package tasks

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"sync"
	"time"
)

var ErrRunning = errors.New("tasks: a task with this key is already running")

// Work is one kind of background work. Run must return soon after ctx ends.
type Work interface {
	Kind() string
	// Key names the subject of the work. A user can run only one task for each key.
	Key() string
	Title() string
	Subtitle() string
	Run(ctx context.Context)
}

type Task struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Key       string    `json:"key"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle"`
	StartedAt time.Time `json:"startedAt"`
}

type running struct {
	Task
	cancel context.CancelFunc
}

// Manager keeps the running tasks of each user in memory.
type Manager struct {
	ctx     context.Context
	changed func(userID int64, tasks []Task)
	wg      sync.WaitGroup

	mu    sync.Mutex
	tasks map[int64][]*running
}

// NewManager makes a manager whose tasks stop when ctx ends. It calls changed under its lock after each start and end of a task.
func NewManager(ctx context.Context, changed func(userID int64, tasks []Task)) *Manager {
	return &Manager{ctx: ctx, changed: changed, tasks: map[int64][]*running{}}
}

func (m *Manager) Start(userID int64, work Work) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slices.ContainsFunc(m.tasks[userID], func(r *running) bool { return r.Key == work.Key() }) {
		return Task{}, ErrRunning
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r := &running{
		Task: Task{
			ID:        rand.Text(),
			Kind:      work.Kind(),
			Key:       work.Key(),
			Title:     work.Title(),
			Subtitle:  work.Subtitle(),
			StartedAt: time.Now(),
		},
		cancel: cancel,
	}
	m.tasks[userID] = append(m.tasks[userID], r)
	m.notify(userID)
	m.wg.Go(func() {
		defer m.remove(userID, r)
		work.Run(ctx)
	})
	return r.Task, nil
}

func (m *Manager) List(userID int64) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.list(userID)
}

// Cancel stops a task of the user and removes it from the list at once. It returns false for an unknown task.
func (m *Manager) Cancel(userID int64, id string) bool {
	m.mu.Lock()
	i := slices.IndexFunc(m.tasks[userID], func(r *running) bool { return r.ID == id })
	var r *running
	if i >= 0 {
		r = m.tasks[userID][i]
	}
	m.mu.Unlock()
	return r != nil && m.remove(userID, r)
}

// Wait blocks until all tasks return.
func (m *Manager) Wait() {
	m.wg.Wait()
}

func (m *Manager) remove(userID int64, r *running) bool {
	r.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.Index(m.tasks[userID], r)
	if i < 0 {
		return false
	}
	m.tasks[userID] = slices.Delete(m.tasks[userID], i, i+1)
	if len(m.tasks[userID]) == 0 {
		delete(m.tasks, userID)
	}
	m.notify(userID)
	return true
}

func (m *Manager) notify(userID int64) {
	if m.changed != nil {
		m.changed(userID, m.list(userID))
	}
}

func (m *Manager) list(userID int64) []Task {
	list := []Task{}
	for _, r := range m.tasks[userID] {
		list = append(list, r.Task)
	}
	return list
}
