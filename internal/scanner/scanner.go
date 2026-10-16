// Package scanner walks scan roots and computes real disk usage.
//
// It records metadata only (SEC-ACC-02): sizes, inode identity and mtimes. It
// never reads file contents, never follows symlinks, never crosses devices and
// treats library packages as opaque items (AGENTS.md §5).
package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/platform"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// Policy is the access-decision interface the scanner consumes (AGENTS.md §3).
type Policy interface {
	RootVerdict(ctx context.Context, root string) (policy.Verdict, error)
	PathVerdict(root, path string) policy.Verdict
	Classify(path string) (domain.Tier, string, bool)
}

// IncrementalIndex lets a scan reuse unchanged subtrees. It is optional: when
// nil, every scan walks the whole tree.
type IncrementalIndex interface {
	// SubtreeMTime returns the mtime recorded for path in the previous scan.
	SubtreeMTime(ctx context.Context, path string) (time.Time, bool, error)
	// CopySubtree copies the previous scan's subtree under prefix into scanID
	// and returns its aggregates.
	CopySubtree(ctx context.Context, scanID, prefix string) (domain.SubtreeTotals, error)
}

// Progress is a structured, non-printing snapshot of scan progress. It is
// delivered to Options.Progress; consumers (TUI, future MCP) decide how to use
// it.
type Progress struct {
	Roots          int    `json:"roots"`
	Directories    int64  `json:"directories"`
	Entries        int64  `json:"entries"`
	AllocatedBytes int64  `json:"allocated_bytes"`
	Current        string `json:"current,omitempty"`
}

// Options configures a scan.
type Options struct {
	ScanID    string
	Roots     []string
	Full      bool
	Workers   int
	BatchSize int
	// Emit, when set, receives batches of entries as they are produced
	// (streaming). When nil, entries are collected in Result.Entries.
	Emit func(ctx context.Context, entries []domain.Entry) error
	// Progress, when set, receives throttled progress snapshots.
	Progress func(Progress)
	// ProgressEvery is how many directories to process between progress
	// snapshots. <= 0 uses a default.
	ProgressEvery int
	Incremental   IncrementalIndex
}

// defaultProgressEvery is how often progress is reported by default.
const defaultProgressEvery = 256

// Result is the structured outcome of a scan.
type Result struct {
	Totals       domain.Totals
	ByClass      map[domain.DataClass]domain.Totals
	ByKind       map[domain.Kind]domain.Totals
	Inaccessible []domain.Inaccessible
	Entries      []domain.Entry
	Partial      bool
	Skipped      []string
}

// Scanner walks directories under a policy.
type Scanner struct {
	policy Policy
	now    func() time.Time
}

// New builds a Scanner with the real clock.
func New(p Policy) *Scanner { return &Scanner{policy: p, now: time.Now} }

// NewWithClock builds a Scanner with an injected clock (for tests).
func NewWithClock(p Policy, now func() time.Time) *Scanner {
	return &Scanner{policy: p, now: now}
}

func defaultWorkers() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Scan walks the configured roots and returns the aggregated result.
func (s *Scanner) Scan(ctx context.Context, opts Options) (Result, error) {
	if opts.ScanID == "" {
		return Result{}, fmt.Errorf("%w: scan id is required", domain.ErrUsage)
	}
	if len(opts.Roots) == 0 {
		return Result{}, fmt.Errorf("%w: at least one root is required", domain.ErrUsage)
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers()
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 1000
	}
	progressEvery := opts.ProgressEvery
	if progressEvery <= 0 {
		progressEvery = defaultProgressEvery
	}

	roots := make([]string, 0, len(opts.Roots))
	for _, r := range opts.Roots {
		v, err := s.policy.RootVerdict(ctx, r)
		if err != nil {
			return Result{}, err
		}
		if v.Blocked() || v.NeedsConsent() {
			return Result{}, fmt.Errorf("%w: root %s: %s", domain.ErrBlocked, v.Path, v.Reason)
		}
		roots = append(roots, v.Path)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	w := &walker{
		s:             s,
		opts:          opts,
		ctx:           ctx,
		cancel:        cancel,
		queue:         newDirQueue(),
		workers:       workers,
		batchSize:     batchSize,
		dirs:          make(map[string]*dirAgg),
		seenLinks:     make(map[[2]uint64]struct{}),
		byClass:       make(map[domain.DataClass]domain.Totals),
		byKind:        make(map[domain.Kind]domain.Totals),
		rootDev:       make(map[string]uint64),
		rootCount:     len(roots),
		progressEvery: progressEvery,
	}

	for _, root := range roots {
		w.registerRoot(root)
	}
	for _, root := range roots {
		w.queue.push(dirTask{root: root, path: root})
	}

	// Unblock workers waiting for work as soon as the context is cancelled, so
	// Ctrl+C stops the walk promptly instead of draining the whole tree.
	go func() {
		<-ctx.Done()
		w.queue.close()
	}()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.worker()
		}()
	}
	wg.Wait()

	// A cancelled walk is not a partial result: report it as a cancellation so
	// the caller rolls the in-progress scan back (SEC-COD-04).
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	w.emitFinalProgress()

	if err := w.failed(); err != nil {
		return Result{}, err
	}
	if err := w.flush(); err != nil {
		return Result{}, err
	}

	return w.result(roots), nil
}

// dirAgg accumulates a directory's own and subtree sizes.
type dirAgg struct {
	parent   string
	class    domain.DataClass
	kind     domain.Kind
	mtime    time.Time
	dev      uint64
	inode    uint64
	ownAlloc int64
	ownLogic int64
	subAlloc int64
	subLogic int64
}

type walker struct {
	s             *Scanner
	opts          Options
	ctx           context.Context
	cancel        context.CancelFunc
	queue         *dirQueue
	workers       int
	batchSize     int
	progressEvery int

	mu            sync.Mutex
	batch         []domain.Entry
	entries       []domain.Entry
	dirs          map[string]*dirAgg
	seenLinks     map[[2]uint64]struct{}
	byClass       map[domain.DataClass]domain.Totals
	byKind        map[domain.Kind]domain.Totals
	inaccessible  []domain.Inaccessible
	skipped       []string
	partial       bool
	rootDev       map[string]uint64
	emitted       int64
	progressAlloc int64
	directories   int64
	current       string
	rootCount     int
	err           error
}

func (w *walker) worker() {
	for {
		task, ok := w.queue.pop()
		if !ok {
			return
		}
		if w.ctx.Err() != nil {
			return
		}
		w.processDir(task)
		w.queue.done()
		w.reportProgress(task.path)
		if w.failed() != nil {
			w.cancel()
		}
	}
}

// cancelled reports whether the walk must stop because the context was
// cancelled (Ctrl+C, timeout or an explicit cancel).
func (w *walker) cancelled() bool { return w.ctx.Err() != nil }

// reportProgress aggregates counters and invokes the progress callback when
// it is due. The callback runs outside the walker lock.
func (w *walker) reportProgress(current string) {
	if w.opts.Progress == nil {
		return
	}
	w.mu.Lock()
	w.directories++
	w.current = current
	dirs := w.directories
	due := dirs%int64(w.progressEvery) == 0
	snap := Progress{
		Roots:          w.rootCount,
		Directories:    dirs,
		Entries:        w.emitted,
		AllocatedBytes: w.progressAlloc,
		Current:        w.current,
	}
	w.mu.Unlock()
	if due {
		w.opts.Progress(snap)
	}
}

// emitFinalProgress sends a last snapshot regardless of throttling.
func (w *walker) emitFinalProgress() {
	if w.opts.Progress == nil {
		return
	}
	w.mu.Lock()
	snap := Progress{
		Roots:          w.rootCount,
		Directories:    w.directories,
		Entries:        w.emitted,
		AllocatedBytes: w.progressAlloc,
		Current:        w.current,
	}
	w.mu.Unlock()
	w.opts.Progress(snap)
}

func (w *walker) registerRoot(root string) {
	fi, err := os.Lstat(root)
	if err != nil {
		w.recordInaccessible(root, err)
		return
	}
	tier, _, _ := w.s.policy.Classify(root)
	if si, ok := platform.Stat(fi); ok {
		w.mu.Lock()
		w.rootDev[root] = si.Dev
		w.mu.Unlock()
	}
	w.registerDir(root, filepath.Dir(root), fi, tier)
}

// processDir reads a directory and dispatches its children.
func (w *walker) processDir(task dirTask) {
	if w.failed() != nil || w.cancelled() {
		return
	}
	if w.opts.Incremental != nil && !w.opts.Full {
		if w.tryReuse(task) {
			return
		}
	}

	des, err := os.ReadDir(task.path)
	if err != nil {
		w.recordInaccessible(task.path, err)
		return
	}
	for _, de := range des {
		if w.failed() != nil || w.cancelled() {
			return
		}
		w.handleChild(task.root, task.path, de)
	}
}

// tryReuse reuses an unchanged subtree from the previous scan.
func (w *walker) tryReuse(task dirTask) bool {
	fi, err := os.Lstat(task.path)
	if err != nil {
		return false
	}
	prev, ok, err := w.opts.Incremental.SubtreeMTime(w.ctx, task.path)
	if err != nil || !ok || !prev.Equal(fi.ModTime()) {
		return false
	}
	sub, err := w.opts.Incremental.CopySubtree(w.ctx, w.opts.ScanID, task.path)
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addSubtreeTotals(sub)
	w.emitted += sub.Totals.Entries
	w.progressAlloc += sub.Totals.AllocatedBytes
	if parent := w.dirs[filepath.Dir(task.path)]; parent != nil {
		parent.subAlloc += sub.Totals.AllocatedBytes
		parent.subLogic += sub.Totals.LogicalBytes
	}
	// The copied subtree already contains this directory's entry, so drop it
	// from the roll-up to avoid emitting (and counting) it twice.
	delete(w.dirs, task.path)
	return true
}

func (w *walker) handleChild(root, dir string, de os.DirEntry) {
	path := filepath.Join(dir, de.Name())

	v := w.s.policy.PathVerdict(root, path)
	if !v.Allowed() {
		if v.Blocked() {
			w.recordSkipped(path, v.Reason)
		}
		return
	}

	fi, err := os.Lstat(path)
	if err != nil {
		w.recordInaccessible(path, err)
		return
	}

	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		w.recordLeaf(path, dir, fi, domain.KindSymlink, v.Tier, "")
	case isLibraryPackage(path) && fi.IsDir():
		w.recordOpaquePackage(path, dir, fi, v.Tier)
	case fi.IsDir():
		if w.crossDevice(root, fi) {
			w.recordSkipped(path, "different device")
			return
		}
		w.registerDir(path, dir, fi, v.Tier)
		w.queue.push(dirTask{root: root, path: path})
	default:
		if w.crossDevice(root, fi) {
			w.recordSkipped(path, "different device")
			return
		}
		w.recordLeaf(path, dir, fi, domain.KindFile, v.Tier, "")
	}
}

func (w *walker) crossDevice(root string, fi os.FileInfo) bool {
	si, ok := platform.Stat(fi)
	if !ok {
		return false
	}
	w.mu.Lock()
	dev, known := w.rootDev[root]
	w.mu.Unlock()
	return known && si.Dev != dev
}

func (w *walker) registerDir(path, parent string, fi os.FileInfo, tier domain.Tier) {
	si, _ := platform.Stat(fi)
	kind := domain.KindDir
	if isAppBundle(path) {
		kind = domain.KindApp
	}
	class := classFor(path, tier, kind)
	agg := &dirAgg{
		parent:   parent,
		class:    class,
		kind:     kind,
		mtime:    fi.ModTime(),
		dev:      si.Dev,
		inode:    si.Inode,
		ownAlloc: si.AllocatedBytes,
		ownLogic: si.LogicalBytes,
		subAlloc: si.AllocatedBytes,
		subLogic: si.LogicalBytes,
	}
	w.mu.Lock()
	w.dirs[path] = agg
	w.mu.Unlock()
}

func (w *walker) recordLeaf(path, parent string, fi os.FileInfo, kind domain.Kind, tier domain.Tier, bundleID string) {
	si, _ := platform.Stat(fi)

	// Hardlink dedup by (dev, inode) when the link count exceeds one.
	if kind == domain.KindFile && si.Nlink > 1 {
		key := [2]uint64{si.Dev, si.Inode}
		w.mu.Lock()
		_, seen := w.seenLinks[key]
		if !seen {
			w.seenLinks[key] = struct{}{}
		}
		w.mu.Unlock()
		if seen {
			return
		}
	}

	class := classFor(path, tier, kind)
	entry := domain.Entry{
		EntryID:        domain.ComputeEntryID(w.opts.ScanID, path),
		ScanID:         w.opts.ScanID,
		Path:           path,
		Parent:         parent,
		AllocatedBytes: si.AllocatedBytes,
		LogicalBytes:   si.LogicalBytes,
		MTime:          fi.ModTime(),
		IsDir:          false,
		Kind:           kind,
		DataClass:      class,
		BundleID:       bundleID,
		Dev:            si.Dev,
		Inode:          si.Inode,
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.addLeafTotals(class, kind, si.AllocatedBytes, si.LogicalBytes)
	if agg := w.dirs[parent]; agg != nil {
		agg.subAlloc += si.AllocatedBytes
		agg.subLogic += si.LogicalBytes
	}
	w.emitLocked(entry)
}

func (w *walker) recordOpaquePackage(path, parent string, fi os.FileInfo, tier domain.Tier) {
	alloc, logical, err := w.sumOpaque(path)
	if err != nil {
		// A cancellation is not an inaccessible path.
		if w.cancelled() {
			return
		}
		w.recordInaccessible(path, err)
		return
	}
	si, _ := platform.Stat(fi)
	class := classFor(path, tier, domain.KindPackage)
	entry := domain.Entry{
		EntryID:        domain.ComputeEntryID(w.opts.ScanID, path),
		ScanID:         w.opts.ScanID,
		Path:           path,
		Parent:         parent,
		AllocatedBytes: alloc,
		LogicalBytes:   logical,
		MTime:          fi.ModTime(),
		IsDir:          true,
		Kind:           domain.KindPackage,
		DataClass:      class,
		Dev:            si.Dev,
		Inode:          si.Inode,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.addLeafTotals(class, domain.KindPackage, alloc, logical)
	if agg := w.dirs[parent]; agg != nil {
		agg.subAlloc += alloc
		agg.subLogic += logical
	}
	w.emitLocked(entry)
}

// sumOpaque sums a library package's size without emitting its contents.
func (w *walker) sumOpaque(root string) (alloc, logical int64, err error) {
	des, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, err
	}
	for _, de := range des {
		if err := w.ctx.Err(); err != nil {
			return 0, 0, err
		}
		p := filepath.Join(root, de.Name())
		fi, lerr := os.Lstat(p)
		if lerr != nil {
			continue
		}
		si, _ := platform.Stat(fi)
		if fi.Mode()&os.ModeSymlink != 0 {
			alloc += si.AllocatedBytes
			logical += si.LogicalBytes
			continue
		}
		if fi.IsDir() && !isLibraryPackage(p) {
			subA, subL, serr := w.sumOpaque(p)
			if serr != nil {
				continue
			}
			alloc += subA
			logical += subL
			continue
		}
		alloc += si.AllocatedBytes
		logical += si.LogicalBytes
	}
	return alloc, logical, nil
}

func (w *walker) addLeafTotals(class domain.DataClass, kind domain.Kind, alloc, logical int64) {
	w.progressAlloc += alloc
	t := w.byClass[class]
	t.Entries++
	t.AllocatedBytes += alloc
	t.LogicalBytes += logical
	w.byClass[class] = t

	k := w.byKind[kind]
	k.Entries++
	k.AllocatedBytes += alloc
	k.LogicalBytes += logical
	w.byKind[kind] = k
}

func (w *walker) addSubtreeTotals(sub domain.SubtreeTotals) {
	for class, t := range sub.ByClass {
		cur := w.byClass[class]
		cur.Entries += t.Entries
		cur.AllocatedBytes += t.AllocatedBytes
		cur.LogicalBytes += t.LogicalBytes
		w.byClass[class] = cur
	}
	for kind, t := range sub.ByKind {
		cur := w.byKind[kind]
		cur.Entries += t.Entries
		cur.AllocatedBytes += t.AllocatedBytes
		cur.LogicalBytes += t.LogicalBytes
		w.byKind[kind] = cur
	}
}

func (w *walker) emitLocked(e domain.Entry) {
	w.batch = append(w.batch, e)
	w.emitted++
	if len(w.batch) >= w.batchSize {
		if err := w.flushLocked(); err != nil {
			w.err = err
		}
	}
}

// failed reports whether the walker has recorded a fatal error.
func (w *walker) failed() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *walker) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flushLocked()
}

func (w *walker) flushLocked() error {
	if len(w.batch) == 0 {
		return nil
	}
	if w.opts.Emit != nil {
		if err := w.opts.Emit(w.ctx, w.batch); err != nil {
			w.err = err
			return err
		}
	} else {
		w.entries = append(w.entries, w.batch...)
	}
	w.batch = w.batch[:0]
	return nil
}

func (w *walker) recordInaccessible(path string, err error) {
	var reason string
	switch {
	case errors.Is(err, os.ErrPermission):
		reason = "permission denied (TCC?)"
	default:
		reason = err.Error()
	}
	w.mu.Lock()
	w.inaccessible = append(w.inaccessible, domain.Inaccessible{Path: path, Reason: reason})
	w.partial = true
	w.mu.Unlock()
}

func (w *walker) recordSkipped(path, reason string) {
	w.mu.Lock()
	w.skipped = append(w.skipped, fmt.Sprintf("%s (%s)", path, reason))
	w.mu.Unlock()
}

// result rolls up subtree sizes, emits directory entries and assembles Result.
func (w *walker) result(roots []string) Result {
	w.mu.Lock()
	dirs := make([]string, 0, len(w.dirs))
	for p := range w.dirs {
		dirs = append(dirs, p)
	}
	// Deepest first so children are complete before their parent.
	sort.Slice(dirs, func(i, j int) bool {
		if len(dirs[i]) != len(dirs[j]) {
			return len(dirs[i]) > len(dirs[j])
		}
		return dirs[i] > dirs[j]
	})

	var total domain.Totals
	for _, p := range dirs {
		agg := w.dirs[p]
		if parent := w.dirs[agg.parent]; parent != nil {
			parent.subAlloc += agg.subAlloc
			parent.subLogic += agg.subLogic
		}
	}
	w.mu.Unlock()

	// Emit directory entries (subtree sizes) after the rollup.
	var dirEntries []domain.Entry
	w.mu.Lock()
	for _, p := range dirs {
		agg := w.dirs[p]
		dirEntries = append(dirEntries, domain.Entry{
			EntryID:        domain.ComputeEntryID(w.opts.ScanID, p),
			ScanID:         w.opts.ScanID,
			Path:           p,
			Parent:         agg.parent,
			AllocatedBytes: agg.subAlloc,
			LogicalBytes:   agg.subLogic,
			MTime:          agg.mtime,
			IsDir:          true,
			Kind:           agg.kind,
			DataClass:      agg.class,
			Dev:            agg.dev,
			Inode:          agg.inode,
		})
	}
	w.batch = append(w.batch, dirEntries...)
	w.emitted += int64(len(dirEntries))
	_ = w.flushLocked()
	entries := w.entries
	inaccessible := w.inaccessible
	skipped := w.skipped
	partial := w.partial
	byClass := w.byClass
	byKind := w.byKind
	emitted := w.emitted
	w.mu.Unlock()

	total.Entries = emitted
	for _, root := range roots {
		if agg := w.dirs[root]; agg != nil {
			total.AllocatedBytes += agg.subAlloc
			total.LogicalBytes += agg.subLogic
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	return Result{
		Totals:       total,
		ByClass:      byClass,
		ByKind:       byKind,
		Inaccessible: inaccessible,
		Entries:      entries,
		Partial:      partial,
		Skipped:      skipped,
	}
}
