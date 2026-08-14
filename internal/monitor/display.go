package monitor

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/data-cleaner/internal/config"
)

func PrintStatus(cfg *config.Config, scheduler *Scheduler) {
	statuses := scheduler.GetStatus()

	if len(statuses) == 0 {
		fmt.Println("No monitor tasks configured.")
		return
	}

	running, pid := IsDaemonRunning(cfg)
	daemonStatus := "stopped"
	if running {
		daemonStatus = fmt.Sprintf("running (PID %d)", pid)
	}

	fmt.Printf("Monitor Daemon: %s\n\n", daemonStatus)
	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("%-20s %-10s %-20s %-20s %-10s %-10s\n",
		"TASK", "RUNNING", "LAST RUN", "NEXT RUN", "DQI", "ALERTS")
	fmt.Println(strings.Repeat("-", 100))

	for _, st := range statuses {
		lastRun := "-"
		if st.LastRunTime != nil {
			lastRun = st.LastRunTime.Format("2006-01-02 15:04:05")
		}

		nextRun := "-"
		if st.NextRunTime != nil {
			nextRun = st.NextRunTime.Format("2006-01-02 15:04:05")
		}

		isRunning := "no"
		if st.Running {
			isRunning = "yes"
		}

		dqi := "-"
		if st.LastDQI > 0 {
			dqi = fmt.Sprintf("%.1f", st.LastDQI)
		}

		alerts := fmt.Sprintf("%d", st.ActiveAlerts)

		fmt.Printf("%-20s %-10s %-20s %-20s %-10s %-10s\n",
			st.Name, isRunning, lastRun, nextRun, dqi, alerts)
	}
	fmt.Println(strings.Repeat("-", 100))

	channelStates := scheduler.GetNotifier().GetChannelStates()
	if len(channelStates) > 0 {
		fmt.Println("\nNotification Channels:")
		for name, degraded := range channelStates {
			status := "healthy"
			if degraded {
				status = "DEGRADED"
			}
			fmt.Printf("  %-20s %s\n", name, status)
		}
	}

	fmt.Println()
	printBaselineInfo(cfg, scheduler)
}

func printBaselineInfo(cfg *config.Config, scheduler *Scheduler) {
	bm := scheduler.GetBaselineManager()
	stateMgr := scheduler.GetStateManager()

	var hasBaseline bool
	for _, task := range cfg.Monitor.Tasks {
		for _, rule := range task.Rules {
			if rule.Mode == "dynamic_baseline" {
				hasBaseline = true
				break
			}
		}
	}
	if !hasBaseline {
		return
	}

	fmt.Println("Dynamic Baselines:")
	fmt.Println(strings.Repeat("-", 80))

	for _, task := range cfg.Monitor.Tasks {
		for _, rule := range task.Rules {
			if rule.Mode != "dynamic_baseline" {
				continue
			}

			st := stateMgr.GetState(task.Name, rule.Name)
			mean, std, dev, hasData := bm.GetBaselineInfo(task.Name, rule.Name)

			if !hasData && st.BaselineMean == 0 {
				fmt.Printf("  %-15s %-20s  collecting data (insufficient samples)\n", task.Name, rule.Name)
				continue
			}

			if st.BaselineMean != 0 || st.BaselineStd != 0 {
				mean = st.BaselineMean
				std = st.BaselineStd
				dev = st.BaselineDev
			}

			currentVal := st.LastValue
			deviationStr := formatDeviation(dev)

			if std > 0 {
				fmt.Printf("  %-15s %-20s  %s: %.1f (baseline: %.1f±%.1f, deviation: %s)\n",
					task.Name, rule.Name, rule.Metric, currentVal, mean, std, deviationStr)
			} else {
				fmt.Printf("  %-15s %-20s  %s: %.1f (baseline: %.1f, static fallback)\n",
					task.Name, rule.Name, rule.Metric, currentVal, mean)
			}
		}
	}
	fmt.Println(strings.Repeat("-", 80))
}

func formatDeviation(dev float64) string {
	if dev == 0 {
		return "0.0σ"
	}
	sign := "+"
	if dev < 0 {
		sign = ""
	}
	if math.Abs(dev) >= 100 {
		return fmt.Sprintf("%s%.0fσ", sign, dev)
	}
	return fmt.Sprintf("%s%.1fσ", sign, dev)
}

func PrintAlerts(scheduler *Scheduler) {
	alerts := scheduler.GetAlerts()

	if len(alerts) == 0 {
		fmt.Println("No active alerts.")
		return
	}

	fmt.Println(strings.Repeat("-", 130))
	fmt.Printf("%-15s %-20s %-12s %-10s %-10s %-20s %-15s\n",
		"TASK", "RULE", "STATE", "CURRENT", "ESC.LVL", "LAST EVAL", "BASELINE")
	fmt.Println(strings.Repeat("-", 130))

	for _, a := range alerts {
		lastEval := a.LastEvalTime.Format("2006-01-02 15:04:05")

		stateStr := string(a.State)
		if a.State == StateSUPPRESSED {
			stateStr = fmt.Sprintf("SUPPRESSED(%s)", a.SuppressedBy)
		}

		escLevel := "-"
		if a.EscalationLevel > 0 {
			escLevel = fmt.Sprintf("L%d", a.EscalationLevel)
		}

		baseline := "-"
		if a.BaselineMean != 0 || a.BaselineStd != 0 {
			if a.BaselineStd > 0 {
				baseline = fmt.Sprintf("%.1f±%.1f %s", a.BaselineMean, a.BaselineStd, formatDeviation(a.BaselineDev))
			} else {
				baseline = fmt.Sprintf("%.1f (static)", a.BaselineMean)
			}
		}

		fmt.Printf("%-15s %-20s %-12s %-10.2f %-10s %-20s %-15s\n",
			a.TaskName, a.RuleName, stateStr, a.LastValue, escLevel, lastEval, baseline)
	}
	fmt.Println(strings.Repeat("-", 130))

	stateMgr := scheduler.GetStateManager()
	allStates := stateMgr.AllStates()
	firingCount := 0
	pendingCount := 0
	suppressedCount := 0
	for _, s := range allStates {
		switch s.State {
		case StateFIRING:
			firingCount++
		case StatePENDING:
			pendingCount++
		case StateSUPPRESSED:
			suppressedCount++
		}
	}
	fmt.Printf("\nSummary: %d FIRING, %d PENDING, %d SUPPRESSED\n", firingCount, pendingCount, suppressedCount)
}

func PrintHistory(cfg *config.Config, scheduler *Scheduler, taskName string, lastN int) {
	results := scheduler.GetHistory(taskName, lastN)

	if len(results) == 0 {
		fmt.Printf("No scan history for task '%s'.\n", taskName)
		return
	}

	fmt.Printf("DQI History for task '%s' (last %d scans):\n\n", taskName, len(results))
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-25s %-8s %-14s %-14s %-14s %-14s\n",
		"TIMESTAMP", "DQI", "COMPLETENESS", "CONSISTENCY", "ACCURACY", "VALIDITY")
	fmt.Println(strings.Repeat("-", 80))

	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		ts := r.Timestamp.Format("2006-01-02 15:04:05")
		comp := "-"
		if v, ok := r.Dimensions["completeness"]; ok {
			comp = fmt.Sprintf("%.1f", v)
		}
		cons := "-"
		if v, ok := r.Dimensions["consistency"]; ok {
			cons = fmt.Sprintf("%.1f", v)
		}
		acc := "-"
		if v, ok := r.Dimensions["accuracy"]; ok {
			acc = fmt.Sprintf("%.1f", v)
		}
		val := "-"
		if v, ok := r.Dimensions["validity"]; ok {
			val = fmt.Sprintf("%.1f", v)
		}

		fmt.Printf("%-25s %-8.1f %-14s %-14s %-14s %-14s\n",
			ts, r.DQI, comp, cons, acc, val)
	}
	fmt.Println(strings.Repeat("-", 80))

	fmt.Println("\nDQI Trend:")
	for i := len(results) - 1; i >= 0; i-- {
		r := results[i]
		ts := r.Timestamp.Format("01-02 15:04")
		barLen := int(r.DQI / 2)
		bar := strings.Repeat("█", barLen)
		if r.DQI >= 80 {
			bar = "\033[32m" + bar + "\033[0m"
		} else if r.DQI >= 60 {
			bar = "\033[33m" + bar + "\033[0m"
		} else {
			bar = "\033[31m" + bar + "\033[0m"
		}
		fmt.Printf("  %s %5.1f %s\n", ts, r.DQI, bar)
	}
}

func PrintManualScanResult(result *ScanResult) {
	if result == nil {
		return
	}

	fmt.Printf("\nScan Result for task '%s':\n", result.TaskName)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("  Timestamp:     %s\n", result.Timestamp.Format(time.RFC3339))
	fmt.Printf("  DQI:           %.1f\n", result.DQI)
	fmt.Printf("  Total Rows:    %d\n", result.TotalRows)
	fmt.Printf("  Total Columns: %d\n", result.TotalColumns)

	fmt.Println("\n  Dimension Scores:")
	for dim, score := range result.Dimensions {
		fmt.Printf("    %-15s %.1f\n", dim, score)
	}

	if len(result.NullRates) > 0 {
		fmt.Println("\n  Null Rates:")
		for col, rate := range result.NullRates {
			fmt.Printf("    %-15s %.1f%%\n", col, rate)
		}
	}

	if len(result.AnomalyCounts) > 0 {
		fmt.Println("\n  Anomaly Counts:")
		for col, count := range result.AnomalyCounts {
			fmt.Printf("    %-15s %d\n", col, count)
		}
	}
}

func Fprintf(w *os.File, format string, args ...interface{}) {
	fmt.Fprintf(w, format, args...)
}
