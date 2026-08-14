package audit

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

type AuditEntry struct {
	Dataset   string `json:"dataset"`
	RowIdx    int    `json:"row_idx"`
	Column    string `json:"column"`
	OldValue  string `json:"old_value"`
	NewValue  string `json:"new_value"`
	Rule      string `json:"rule"`
	Timestamp string `json:"timestamp"`
}

type Logger struct {
	file    *os.File
	mu      sync.Mutex
	entries []AuditEntry
}

func NewLogger(filepath string) (*Logger, error) {
	f, err := os.Create(filepath)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f}, nil
}

func NewMemoryLogger() *Logger {
	return &Logger{}
}

func (l *Logger) Log(entry AuditEntry) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format(time.RFC3339)
	}

	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()

	if l.file != nil {
		data, _ := json.Marshal(entry)
		l.file.Write(data)
		l.file.Write([]byte("\n"))
	}
}

func (l *Logger) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]AuditEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

func (l *Logger) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

func (l *Logger) WriteToFile(filepath string) error {
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	for _, entry := range l.entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}
