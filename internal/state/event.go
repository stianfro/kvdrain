package state

import (
	"encoding/json"
	"sync"
	"time"
)

const APIVersion = "kvdrain.io/v1alpha1"

type ObjectRef struct {
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
}

type Event struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Time       time.Time      `json:"time"`
	RunID      string         `json:"runID"`
	Type       string         `json:"type"`
	Node       string         `json:"node,omitempty"`
	Object     *ObjectRef     `json:"object,omitempty"`
	State      string         `json:"state"`
	Message    string         `json:"message,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

// Reducer suppresses identical object transitions for both plain and JSON output.
type Reducer struct {
	mu   sync.Mutex
	last map[string]string
}

func NewReducer() *Reducer { return &Reducer{last: make(map[string]string)} }

func (r *Reducer) Accept(e Event) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := e.Type + ":" + e.Node
	if e.Object != nil {
		key += ":" + e.Object.Kind + ":" + e.Object.Namespace + ":" + e.Object.Name
	}
	details, _ := json.Marshal(e.Details)
	value := e.State + "\x00" + e.Message + "\x00" + string(details)
	if r.last[key] == value {
		return false
	}
	r.last[key] = value
	return true
}
