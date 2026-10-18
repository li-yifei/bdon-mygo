package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// nativeDeck is the only service exposed to the bdon origin. Its methods accept
// JSON and dataset identities; the page never supplies filesystem paths.
type nativeDeck struct {
	rust         *rustDeck
	mu           sync.RWMutex
	datasets     map[string]string
	jobs         map[string]nativeSearchJob
	nativeSolver bool
	openSettings func()
	// mismatch is the last model gap Prepare refused, or nil.
	mismatch *engineModelMismatch
}

// engineModelMismatch names an engine whose model differs from the page's.
// The page then keeps its own solver, so results match the site.
type engineModelMismatch struct {
	Engine, Page string
}

// errEngineModelMismatch prefixes the error the page script recognizes to
// fall back to the page's own solver.
const errEngineModelMismatch = "engine-model-mismatch"

func (s *nativeDeck) modelMismatch() *engineModelMismatch {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mismatch
}

func (s *nativeDeck) UseNativeSolver() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nativeSolver
}

type nativeSearchJob struct {
	rust *rustDeck
	id   string
}

// The opaque desktop token binds every poll/cancel to the engine that started
// the search, including when settings switch engines during a running job.
func (s *nativeDeck) RecommendStart(datasetID, accountJSON, requestJSON string) (string, error) {
	if len(accountJSON) > 16<<20 || len(requestJSON) > 1<<20 || !json.Valid([]byte(accountJSON)) || !json.Valid([]byte(requestJSON)) {
		return "", errors.New("invalid native solver input")
	}
	s.mu.RLock()
	path := s.datasets[datasetID]
	s.mu.RUnlock()
	if path == "" {
		return "", errors.New("native dataset is not prepared")
	}
	rust := s.engine()
	if rust.jobStart == nil {
		return "", nil
	}
	a, b, c := cstring(path), cstring(accountJSON), cstring(requestJSON)
	var started struct {
		JobID string `json:"jobId"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(rust.take(rust.jobStart(&a[0], &b[0], &c[0]))), &started); err != nil {
		return "", err
	}
	if started.Error != "" {
		return "", errors.New(started.Error)
	}
	if started.JobID == "" {
		return "", errors.New("native job identity missing")
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		id := cstring(started.JobID)
		rust.take(rust.jobCancel(&id[0]))
		return "", err
	}
	token := hex.EncodeToString(bytes[:])
	s.mu.Lock()
	if s.jobs == nil {
		s.jobs = map[string]nativeSearchJob{}
	}
	s.jobs[token] = nativeSearchJob{rust: rust, id: started.JobID}
	s.mu.Unlock()
	return token, nil
}

func (s *nativeDeck) RecommendPoll(token string) (string, error) {
	s.mu.RLock()
	job, ok := s.jobs[token]
	s.mu.RUnlock()
	if !ok {
		return "", errors.New("native search job unavailable")
	}
	id := cstring(job.id)
	result := job.rust.take(job.rust.jobPoll(&id[0]))
	var state struct {
		Done bool `json:"done"`
	}
	if err := json.Unmarshal([]byte(result), &state); err != nil {
		return "", err
	}
	if state.Done {
		s.mu.Lock()
		delete(s.jobs, token)
		s.mu.Unlock()
	}
	return result, nil
}

func (s *nativeDeck) RecommendCancel(token string) {
	s.mu.Lock()
	job, ok := s.jobs[token]
	delete(s.jobs, token)
	s.mu.Unlock()
	if ok {
		id := cstring(job.id)
		job.rust.take(job.rust.jobCancel(&id[0]))
	}
}

type nativeReady struct {
	DatasetID        string  `json:"datasetId"`
	InitMS           float64 `json:"initMs"`
	CapabilitiesJSON string  `json:"capabilitiesJson"`
}

// Each invocation retains its engine for the full FFI call, including freeing
// the result with the same library that allocated it.
func (s *nativeDeck) engine() *rustDeck {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rust
}
func (s *nativeDeck) setEngine(rust *rustDeck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rust = rust
}

func nativeDatasetPath(id string) (string, error) {
	if len(id) != 64 {
		return "", errors.New("invalid dataset identity")
	}
	if _, err := hex.DecodeString(id); err != nil || id != strings.ToLower(id) {
		return "", errors.New("invalid dataset identity")
	}
	// Verified datasets are re-creatable from the page: they live in the cache.
	return productCachePath("datasets", id+".json"), nil
}
func (s *nativeDeck) Prepare(dataText, expectedSHA, modelCommit string) (nativeReady, error) {
	start := time.Now()
	if len(dataText) > 64<<20 || !json.Valid([]byte(dataText)) {
		return nativeReady{}, errors.New("invalid deck data JSON")
	}
	digest := sha256.Sum256([]byte(dataText))
	id := hex.EncodeToString(digest[:])
	if id != expectedSHA {
		return nativeReady{}, errors.New("deck data SHA-256 mismatch")
	}
	path, err := nativeDatasetPath(id)
	if err != nil {
		return nativeReady{}, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nativeReady{}, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".dataset-")
	if err != nil {
		return nativeReady{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(dataText)
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return nativeReady{}, err
	}
	rust := s.engine()
	p := cstring(path)
	infoText := rust.take(rust.datasetInfo(&p[0]))
	var info struct {
		DatasetID   string  `json:"datasetId"`
		ModelCommit *string `json:"modelCommit"`
		Error       string  `json:"error"`
	}
	if err = json.Unmarshal([]byte(infoText), &info); err != nil {
		return nativeReady{}, err
	}
	if info.Error != "" {
		return nativeReady{}, errors.New(info.Error)
	}
	if info.DatasetID != id {
		return nativeReady{}, errors.New("native dataset identity mismatch")
	}
	if modelCommit != "" && (info.ModelCommit == nil || *info.ModelCommit != modelCommit) {
		return nativeReady{}, errors.New("deck data model commit mismatch")
	}
	// An engine of another model than the page's would rank teams differently.
	// Engines that do not declare their model are the user's explicit choice.
	if modelCommit != "" && rust.model != "" && rust.model != modelCommit {
		s.mu.Lock()
		s.mismatch = &engineModelMismatch{Engine: rust.model, Page: modelCommit}
		s.mu.Unlock()
		return nativeReady{}, fmt.Errorf("%s: the engine implements %s, the page %s", errEngineModelMismatch, shortCommit(rust.model), shortCommit(modelCommit))
	}
	s.mu.Lock()
	if s.datasets == nil {
		s.datasets = map[string]string{}
	}
	s.datasets[id] = path
	s.mismatch = nil
	s.mu.Unlock()
	return nativeReady{DatasetID: id, InitMS: float64(time.Since(start).Microseconds()) / 1000, CapabilitiesJSON: rust.take(rust.capabilities())}, nil
}
func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func (s *nativeDeck) Recommend(datasetID, accountJSON, requestJSON string) (string, error) {
	if len(accountJSON) > 16<<20 || len(requestJSON) > 1<<20 || !json.Valid([]byte(accountJSON)) || !json.Valid([]byte(requestJSON)) {
		return "", errors.New("invalid native solver input")
	}
	s.mu.RLock()
	path := s.datasets[datasetID]
	s.mu.RUnlock()
	if path == "" {
		return "", errors.New("native dataset is not prepared")
	}
	a, b, c := cstring(path), cstring(accountJSON), cstring(requestJSON)
	rust := s.engine()
	result := rust.take(rust.recommendAccount(&a[0], &b[0], &c[0]))
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &envelope) != nil {
		return "", errors.New("invalid native solver result")
	}
	if raw := envelope["error"]; raw != nil {
		var message string
		_ = json.Unmarshal(raw, &message)
		return "", fmt.Errorf("native solver: %s", message)
	}
	return result, nil
}
func (s *nativeDeck) OpenSettings() {
	if s.openSettings != nil {
		s.openSettings()
	}
}
func (r *rustDeck) take(result *byte) string {
	if result == nil {
		return `{"error":"native returned null"}`
	}
	defer r.free(result)
	size := r.stringLen(result)
	if size > 64<<20 {
		return `{"error":"native result exceeds size limit"}`
	}
	// The ABI reports the allocation's actual length and keeps it alive until free.
	return string(unsafe.Slice(result, int(size)))
}
