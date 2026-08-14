package monitor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/data-cleaner/internal/config"
)

type AlertContext struct {
	RuleName         string   `json:"rule_name"`
	CurrentValue     float64  `json:"current_value"`
	Threshold        float64  `json:"threshold"`
	Source           string   `json:"source"`
	TaskName         string   `json:"task_name"`
	Operator         string   `json:"operator"`
	State            string   `json:"state"`
	PreviousState    string   `json:"previous_state"`
	FiredAt          string   `json:"fired_at,omitempty"`
	Timestamp        string   `json:"timestamp"`
	AffectedColumns  []string `json:"affected_columns,omitempty"`
	Template         string   `json:"-"`
	EscalationLevel  int      `json:"escalation_level,omitempty"`
	Duration         string   `json:"duration,omitempty"`
	NotifyCount      int      `json:"notify_count,omitempty"`
	BaselineMean     float64  `json:"baseline_mean,omitempty"`
	BaselineStd      float64  `json:"baseline_std,omitempty"`
	BaselineDev      float64  `json:"baseline_dev,omitempty"`
	SuppressionLifted bool    `json:"suppression_lifted,omitempty"`
	SuppressedBy     string   `json:"suppressed_by,omitempty"`
}

type AggregatedAlert struct {
	TaskName  string         `json:"task_name"`
	Source    string         `json:"source"`
	Timestamp string         `json:"timestamp"`
	Alerts    []AlertContext `json:"alerts"`
}

type ChannelState struct {
	Name        string
	URL         string
	Timeout     time.Duration
	IsDegraded  bool
	LastProbeAt time.Time
	mu          sync.Mutex
}

type Notifier struct {
	channels map[string]*ChannelState
	failedLog *os.File
	logMu     sync.Mutex
	cacheDir  string
}

func NewNotifier(cfg *config.Config, cacheDir string) *Notifier {
	n := &Notifier{
		channels: make(map[string]*ChannelState),
		cacheDir: cacheDir,
	}

	dir := filepath.Join(cacheDir, "monitor")
	os.MkdirAll(dir, 0755)

	f, err := os.OpenFile(filepath.Join(dir, "notify_failed.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		n.failedLog = f
	}

	for _, ch := range cfg.Monitor.Channels {
		timeout := 10 * time.Second
		if ch.Timeout != "" {
			if d, err := time.ParseDuration(ch.Timeout); err == nil {
				timeout = d
			}
		}
		n.channels[ch.Name] = &ChannelState{
			Name:    ch.Name,
			URL:     ch.URL,
			Timeout: timeout,
		}
	}

	return n
}

func (n *Notifier) Close() {
	if n.failedLog != nil {
		n.failedLog.Close()
	}
}

func (n *Notifier) Notify(ctx AlertContext, channelNames []string) error {
	var lastErr error
	for _, chName := range channelNames {
		ch, ok := n.channels[chName]
		if !ok {
			log.Printf("[monitor] channel '%s' not found", chName)
			continue
		}

		ch.mu.Lock()
		isDegraded := ch.IsDegraded
		ch.mu.Unlock()

		if isDegraded {
			n.logFailed(ch, ctx, "channel in degraded mode")
			continue
		}

		if err := n.sendWithRetry(ch, ctx); err != nil {
			lastErr = err
			ch.mu.Lock()
			ch.IsDegraded = true
			ch.mu.Unlock()
			n.logFailed(ch, ctx, err.Error())
		}
	}
	return lastErr
}

func (n *Notifier) NotifyAggregated(agg AggregatedAlert, channelNames []string) error {
	var lastErr error
	for _, chName := range channelNames {
		ch, ok := n.channels[chName]
		if !ok {
			continue
		}

		ch.mu.Lock()
		isDegraded := ch.IsDegraded
		ch.mu.Unlock()

		if isDegraded {
			n.logAggFailed(ch, agg, "channel in degraded mode")
			continue
		}

		if err := n.sendAggWithRetry(ch, agg); err != nil {
			lastErr = err
			ch.mu.Lock()
			ch.IsDegraded = true
			ch.mu.Unlock()
			n.logAggFailed(ch, agg, err.Error())
		}
	}
	return lastErr
}

func (n *Notifier) sendWithRetry(ch *ChannelState, ctx AlertContext) error {
	var lastErr error
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			time.Sleep(delays[attempt-1])
		}
		if err := n.send(ch, ctx); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (n *Notifier) sendAggWithRetry(ch *ChannelState, agg AggregatedAlert) error {
	var lastErr error
	delays := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			time.Sleep(delays[attempt-1])
		}
		if err := n.sendAgg(ch, agg); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

func (n *Notifier) send(ch *ChannelState, ctx AlertContext) error {
	var payload []byte
	var err error

	if ctx.Template != "" {
		rendered, renderErr := RenderTemplate(ctx.Template, ctx)
		if renderErr != nil {
			log.Printf("[monitor] template render error: %v, falling back to JSON", renderErr)
			payload, err = json.Marshal(ctx)
		} else {
			payload = []byte(rendered)
		}
	} else {
		payload, err = json.Marshal(ctx)
	}
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: ch.Timeout}
	resp, err := client.Post(ch.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (n *Notifier) sendAgg(ch *ChannelState, agg AggregatedAlert) error {
	payload, err := json.Marshal(agg)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: ch.Timeout}
	resp, err := client.Post(ch.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (n *Notifier) logFailed(ch *ChannelState, ctx AlertContext, reason string) {
	log.Printf("[monitor] notification failed for channel '%s': %s (rule=%s, task=%s)", ch.Name, reason, ctx.RuleName, ctx.TaskName)

	if n.failedLog != nil {
		n.logMu.Lock()
		defer n.logMu.Unlock()
		entry := fmt.Sprintf("[%s] channel=%s rule=%s task=%s reason=%s\n",
			time.Now().Format(time.RFC3339), ch.Name, ctx.RuleName, ctx.TaskName, reason)
		n.failedLog.WriteString(entry)
	}
}

func (n *Notifier) logAggFailed(ch *ChannelState, agg AggregatedAlert, reason string) {
	log.Printf("[monitor] aggregated notification failed for channel '%s': %s (task=%s, alerts=%d)", ch.Name, reason, agg.TaskName, len(agg.Alerts))

	if n.failedLog != nil {
		n.logMu.Lock()
		defer n.logMu.Unlock()
		entry := fmt.Sprintf("[%s] channel=%s task=%s alerts=%d reason=%s\n",
			time.Now().Format(time.RFC3339), ch.Name, agg.TaskName, len(agg.Alerts), reason)
		n.failedLog.WriteString(entry)
	}
}

func (n *Notifier) ProbeRecovery() {
	for _, ch := range n.channels {
		ch.mu.Lock()
		if !ch.IsDegraded {
			ch.mu.Unlock()
			continue
		}

		if time.Since(ch.LastProbeAt) < 5*time.Minute {
			ch.mu.Unlock()
			continue
		}
		ch.LastProbeAt = time.Now()
		ch.mu.Unlock()

		client := &http.Client{Timeout: ch.Timeout}
		resp, err := client.Get(ch.URL)
		if err != nil {
			continue
		}
		resp.Body.Close()

		ch.mu.Lock()
		ch.IsDegraded = false
		ch.mu.Unlock()
		log.Printf("[monitor] channel '%s' recovered from degraded mode", ch.Name)
	}
}

func RenderTemplate(tmplStr string, ctx AlertContext) (string, error) {
	if tmplStr == "" {
		data, err := json.MarshalIndent(ctx, "", "  ")
		return string(data), err
	}

	tmpl, err := template.New("alert").Parse(tmplStr)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (n *Notifier) GetChannelStates() map[string]bool {
	result := make(map[string]bool)
	for name, ch := range n.channels {
		ch.mu.Lock()
		result[name] = ch.IsDegraded
		ch.mu.Unlock()
	}
	return result
}
