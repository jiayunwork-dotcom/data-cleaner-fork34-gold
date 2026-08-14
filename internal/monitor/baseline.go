package monitor

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/data-cleaner/internal/config"
)

type BaselineDataPoint struct {
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

type BaselineResult struct {
	Mean      float64 `json:"mean"`
	Std       float64 `json:"std"`
	Deviation float64 `json:"deviation"`
	Triggered bool    `json:"triggered"`
	Fallback  bool    `json:"fallback"`
}

type MetricBaseline struct {
	mu       sync.Mutex
	buffer   []BaselineDataPoint
	cap      int
	head     int
	count    int
	sigma    float64
	period   time.Duration
	filePath string
	taskName string
	ruleName string
	metric   string
}

type BaselineManager struct {
	mu       sync.Mutex
	baselines map[string]*MetricBaseline
	cacheDir  string
}

func NewBaselineManager(cacheDir string) *BaselineManager {
	dir := filepath.Join(cacheDir, "monitor")
	os.MkdirAll(dir, 0755)

	bm := &BaselineManager{
		baselines: make(map[string]*MetricBaseline),
		cacheDir:  cacheDir,
	}
	bm.load()
	return bm
}

func baselineKey(taskName, ruleName string) string {
	return taskName + "|" + ruleName
}

func (bm *BaselineManager) GetOrCreateBaseline(taskName, ruleName, metric string, rule config.AlertRule) *MetricBaseline {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	key := baselineKey(taskName, ruleName)
	if bl, ok := bm.baselines[key]; ok {
		return bl
	}

	window := 30
	if rule.BaselineWindow > 0 {
		window = rule.BaselineWindow
	}
	if window < 5 {
		window = 5
	}

	sigma := 3.0
	if rule.BaselineSigma > 0 {
		sigma = rule.BaselineSigma
	}

	var period time.Duration
	if rule.SeasonalPeriod != "" {
		period, _ = time.ParseDuration(rule.SeasonalPeriod)
	}

	dir := filepath.Join(bm.cacheDir, "monitor")
	fp := filepath.Join(dir, "baselines.json")

	bl := &MetricBaseline{
		buffer:   make([]BaselineDataPoint, window),
		cap:      window,
		sigma:    sigma,
		period:   period,
		filePath: fp,
		taskName: taskName,
		ruleName: ruleName,
		metric:   metric,
	}

	bm.baselines[key] = bl
	return bl
}

func (bl *MetricBaseline) AddPoint(value float64, ts time.Time) {
	bl.mu.Lock()
	defer bl.mu.Unlock()

	bl.buffer[bl.head] = BaselineDataPoint{Value: value, Timestamp: ts}
	bl.head = (bl.head + 1) % bl.cap
	if bl.count < bl.cap {
		bl.count++
	}
}

func (bl *MetricBaseline) Evaluate(value float64, operator string, staticThreshold float64) BaselineResult {
	bl.mu.Lock()
	defer bl.mu.Unlock()

	minRequired := 5
	if bl.count < minRequired {
		return BaselineResult{
			Triggered: evaluateCondition(value, operator, staticThreshold),
			Fallback:  true,
			Mean:      staticThreshold,
		}
	}

	points := bl.collectForBaseline(value)
	mean, std := calcBaselineStats(points)

	if std == 0 {
		return BaselineResult{
			Mean:      mean,
			Std:       0,
			Deviation: 0,
			Triggered: evaluateCondition(value, operator, mean),
			Fallback:  true,
		}
	}

	deviation := (value - mean) / std

	triggered := false
	switch operator {
	case "<":
		triggered = value < (mean-bl.sigma*std) || value < staticThreshold
	case ">":
		triggered = value > (mean+bl.sigma*std) || value > staticThreshold
	case "<=":
		triggered = value <= (mean-bl.sigma*std) || value <= staticThreshold
	case ">=":
		triggered = value >= (mean+bl.sigma*std) || value >= staticThreshold
	case "==":
		triggered = math.Abs(deviation) > bl.sigma
	}

	return BaselineResult{
		Mean:      mean,
		Std:       std,
		Deviation: deviation,
		Triggered: triggered,
		Fallback:  false,
	}
}

func (bl *MetricBaseline) collectForBaseline(currentValue float64) []float64 {
	var allPoints []BaselineDataPoint
	for i := 0; i < bl.count; i++ {
		idx := (bl.head - 1 - i + bl.cap) % bl.cap
		allPoints = append(allPoints, bl.buffer[idx])
	}

	var filtered []BaselineDataPoint
	if bl.period > 0 {
		now := time.Now()
		for _, p := range allPoints {
			if bl.sameSeasonalSlot(p.Timestamp, now) {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) < 5 {
			filtered = allPoints
		}
	} else {
		filtered = allPoints
	}

	values := make([]float64, 0, len(filtered))
	for _, p := range filtered {
		values = append(values, p.Value)
	}

	values = removeOutliers(values)
	if len(values) < 2 {
		values = make([]float64, 0, len(filtered))
		for _, p := range filtered {
			values = append(values, p.Value)
		}
	}

	return values
}

func (bl *MetricBaseline) sameSeasonalSlot(a, b time.Time) bool {
	switch bl.period {
	case time.Hour:
		return a.Minute() == b.Minute()
	case 24 * time.Hour:
		return a.Hour() == b.Hour()
	case 168 * time.Hour:
		return a.Weekday() == b.Weekday() && a.Hour() == b.Hour()
	default:
		return true
	}
}

func removeOutliers(values []float64) []float64 {
	if len(values) < 4 {
		return values
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	n := len(sorted)
	q1Idx := n / 4
	q3Idx := (3 * n) / 4
	q1 := sorted[q1Idx]
	q3 := sorted[q3Idx]
	iqr := q3 - q1

	lower := q1 - 1.5*iqr
	upper := q3 + 1.5*iqr

	var result []float64
	for _, v := range values {
		if v >= lower && v <= upper {
			result = append(result, v)
		}
	}
	return result
}

func calcBaselineStats(values []float64) (mean, std float64) {
	if len(values) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))

	if len(values) < 2 {
		return mean, 0
	}

	varSum := 0.0
	for _, v := range values {
		d := v - mean
		varSum += d * d
	}
	std = math.Sqrt(varSum / float64(len(values)))

	return mean, std
}

func (bm *BaselineManager) Persist() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	allData := make(map[string][]BaselineDataPoint)
	for key, bl := range bm.baselines {
		bl.mu.Lock()
		var points []BaselineDataPoint
		for i := 0; i < bl.count; i++ {
			idx := (bl.head - bl.count + i + bl.cap) % bl.cap
			points = append(points, bl.buffer[idx])
		}
		allData[key] = points
		bl.mu.Unlock()
	}

	dir := filepath.Join(bm.cacheDir, "monitor")
	os.MkdirAll(dir, 0755)
	fp := filepath.Join(dir, "baselines.json")

	data, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(fp, data, 0644)
}

func (bm *BaselineManager) load() {
	dir := filepath.Join(bm.cacheDir, "monitor")
	fp := filepath.Join(dir, "baselines.json")

	data, err := os.ReadFile(fp)
	if err != nil {
		return
	}

	var allData map[string][]BaselineDataPoint
	if err := json.Unmarshal(data, &allData); err != nil {
		return
	}

	for key, points := range allData {
		if len(points) == 0 {
			continue
		}

		window := 30
		if len(points) > window {
			points = points[len(points)-window:]
		}

		bl := &MetricBaseline{
			buffer:   make([]BaselineDataPoint, window),
			cap:      window,
			sigma:    3.0,
			filePath: fp,
		}

		for _, p := range points {
			bl.buffer[bl.head] = p
			bl.head = (bl.head + 1) % bl.cap
			if bl.count < bl.cap {
				bl.count++
			}
		}

		bm.baselines[key] = bl
	}
}

func (bm *BaselineManager) GetBaselineInfo(taskName, ruleName string) (mean, std, deviation float64, hasBaseline bool) {
	bm.mu.Lock()
	bl, ok := bm.baselines[baselineKey(taskName, ruleName)]
	bm.mu.Unlock()

	if !ok || bl == nil {
		return 0, 0, 0, false
	}

	bl.mu.Lock()
	defer bl.mu.Unlock()

	if bl.count < 5 {
		return 0, 0, 0, false
	}

	points := make([]float64, 0, bl.count)
	for i := 0; i < bl.count; i++ {
		idx := (bl.head - 1 - i + bl.cap) % bl.cap
		points = append(points, bl.buffer[idx].Value)
	}

	mean, std = calcBaselineStats(points)
	if std > 0 {
		lastIdx := (bl.head - 1 + bl.cap) % bl.cap
		deviation = (bl.buffer[lastIdx].Value - mean) / std
	}

	return mean, std, deviation, true
}
