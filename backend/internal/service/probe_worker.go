package service

import (
	"context"
	"log"
	"sync"
	"time"

	"studyquest/backend/internal/repository"
)

// ProbeWorker is a single-consumer background queue that runs ffprobe against
// episodes to backfill media metadata (duration, codecs, resolution, ...).
//
// Why a serialized queue instead of a worker pool?
// Netdisk providers behind AList (天翼/115/夸克) enforce API rate limits, and
// each probe issues both an AList sign-URL request and several range GETs to
// the cloud CDN. A burst of concurrent probes can therefore trip a 429 / sign
// failure that affects playback too. We process one episode at a time with a
// fixed gap (probeInterval) between probes — conservative by design.
type ProbeWorker struct {
	episodeService EpisodeService
	episodeRepo    repository.EpisodeRepository

	queue   chan uint
	pending map[uint]struct{} // dedupe: an episode already queued is not re-added
	mu      sync.Mutex        // guards pending

	statsMu sync.RWMutex
	stats   ProbeStats
}

// ProbeStats is the progress snapshot polled by the admin UI.
type ProbeStats struct {
	Running         bool   `json:"running"`
	CurrentEpisode  uint   `json:"current_episode_id"`
	CurrentTitle    string `json:"current_title"`
	Total           int    `json:"total"`  // episodes in the current batch
	Done            int    `json:"done"`   // successfully probed
	Failed          int    `json:"failed"` // failed attempts
	LastError       string `json:"last_error"`
	LastFinishedAt  string `json:"last_finished_at"` // RFC3339, empty if never
}

// probeInterval is the mandatory gap between two consecutive ffprobe calls.
// Conservative (3s) so we never burst the netdisk API. Tune here if a faster
// cadence is ever safe.
const probeInterval = 3 * time.Second

const queueCapacity = 200

// NewProbeWorker constructs a (single, process-wide) worker. Start it once
// from main via `go worker.Start(ctx)`.
func NewProbeWorker(es EpisodeService, repo repository.EpisodeRepository) *ProbeWorker {
	return &ProbeWorker{
		episodeService: es,
		episodeRepo:    repo,
		queue:          make(chan uint, queueCapacity),
		pending:        make(map[uint]struct{}),
	}
}

// Start blocks until ctx is canceled, draining the queue. Meant to run in its
// own goroutine.
func (w *ProbeWorker) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-w.queue:
			w.probeOne(ctx, id)
			// Mandatory cooldown before the next probe to avoid netdisk rate
			// limits. Still honor cancellation during the sleep.
			select {
			case <-ctx.Done():
				return
			case <-time.After(probeInterval):
			}
		}
	}
}

// Enqueue adds an episode to the probe queue. It is non-blocking and
// de-duplicates: if the episode is already queued or currently being probed,
// the call is a no-op. Safe to call from import/ingest handlers.
func (w *ProbeWorker) Enqueue(id uint) {
	w.mu.Lock()
	if _, ok := w.pending[id]; ok {
		w.mu.Unlock()
		return
	}
	w.pending[id] = struct{}{}
	w.mu.Unlock()

	// Mark the batch as running if this is the first item.
	w.statsMu.Lock()
	if !w.stats.Running {
		w.stats.Running = true
		w.stats.Total = 0
		w.stats.Done = 0
		w.stats.Failed = 0
	}
	w.stats.Total++
	w.statsMu.Unlock()

	select {
	case w.queue <- id:
	default:
		// Queue full: drop and undo bookkeeping.
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		w.statsMu.Lock()
		w.stats.Total--
		w.statsMu.Unlock()
		log.Printf("[probe-worker] queue full, dropped episode %d", id)
	}
}

// EnqueueBatch resets the batch counters then enqueues all ids. Used by the
// admin "scan missing durations" button.
func (w *ProbeWorker) EnqueueBatch(ids []uint) int {
	w.statsMu.Lock()
	w.stats = ProbeStats{Running: true, Total: len(ids)}
	w.statsMu.Unlock()

	enqueued := 0
	for _, id := range ids {
		w.mu.Lock()
		if _, ok := w.pending[id]; ok {
			w.mu.Unlock()
			continue
		}
		w.pending[id] = struct{}{}
		w.mu.Unlock()
		select {
		case w.queue <- id:
			enqueued++
		default:
			w.mu.Lock()
			delete(w.pending, id)
			w.mu.Unlock()
			w.statsMu.Lock()
			w.stats.Total--
			w.statsMu.Unlock()
			log.Printf("[probe-worker] queue full, dropped episode %d", id)
		}
	}
	return enqueued
}

// Stats returns a snapshot of current progress for the admin UI.
func (w *ProbeWorker) Stats() ProbeStats {
	w.statsMu.RLock()
	defer w.statsMu.RUnlock()
	return w.stats
}

func (w *ProbeWorker) probeOne(ctx context.Context, id uint) {
	// Resolve a friendly title for the progress display.
	title := ""
	if ep, err := w.episodeRepo.FindByID(id); err == nil && ep != nil {
		title = ep.Title
	}

	w.statsMu.Lock()
	w.stats.CurrentEpisode = id
	w.stats.CurrentTitle = title
	w.statsMu.Unlock()

	_, err := w.episodeService.Probe(id)
	w.statsMu.Lock()
	w.stats.LastFinishedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		w.stats.Failed++
		w.stats.LastError = err.Error()
		log.Printf("[probe-worker] episode %d (%s) failed: %v", id, title, err)
	} else {
		w.stats.Done++
		w.stats.LastError = ""
		log.Printf("[probe-worker] episode %d (%s) probed ok", id, title)
	}
	// If the queue has drained, flip running off so the UI stops polling.
	if len(w.queue) == 0 {
		w.stats.Running = false
		w.stats.CurrentEpisode = 0
		w.stats.CurrentTitle = ""
	}
	w.statsMu.Unlock()

	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
}
