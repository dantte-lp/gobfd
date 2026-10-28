//go:build linux

package bfdload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	referenceCPUs       = 8
	referenceMemory     = 8 << 30
	maxAdmissionDepth   = 64
	defaultGCPercentage = 100
	baselineTaskMargin  = 64
)

// ErrAdmission rejects runtime resources that cannot establish the requested envelope.
var ErrAdmission = errors.New("reference resource admission rejected")

// ResourceAdmission covers visible limits, not reserved capacity or qualification.
// The runner must separately verify that its cgroup namespace exposes host ancestors.
type ResourceAdmission struct {
	Environment          Environment      `json:"environment"`
	Ancestors            []CgroupSnapshot `json:"ancestors"`
	Admitted             bool             `json:"admitted"`
	AdditionalTaskBudget uint64           `json:"additional_task_budget"`
}

// AdmitReferenceResources reads actual limits without changing runtime policy.
// Root-only membership or a subtree mount cannot establish the ancestor envelope.
func AdmitReferenceResources(ctx context.Context, sessions int) (ResourceAdmission, error) {
	var result ResourceAdmission
	if sessions < 1 || sessions > sourcePortCapacity {
		return result, fmt.Errorf("invalid admission session count %d: %w", sessions, ErrAdmission)
	}
	result.AdditionalTaskBudget = uint64(sessions) + baselineTaskMargin
	var err error
	result.Environment, err = CaptureEnvironment(ctx)
	if err != nil {
		return result, fmt.Errorf("capture reference runtime: %w: %w", ErrAdmission, err)
	}
	current := result.Environment.Cgroup
	if current == nil || current.MountRoot != "/" || current.MemberPath == "/" {
		return result, fmt.Errorf("cgroup ancestors are hidden or unavailable: %w", ErrAdmission)
	}
	root, err := os.OpenRoot(current.MountPoint)
	if err != nil {
		return result, fmt.Errorf("open reference cgroup hierarchy: %w: %w", ErrAdmission, err)
	}
	result.Ancestors, err = readAdmissionChain(ctx, root, current.MemberPath)
	if joinedErr := errors.Join(err, root.Close()); joinedErr != nil {
		return result, fmt.Errorf("read reference cgroup hierarchy: %w: %w", ErrAdmission, joinedErr)
	}
	if err := validateReferenceResources(result.Environment, result.Ancestors, result.AdditionalTaskBudget); err != nil {
		return result, err
	}
	result.Admitted = true
	return result, nil
}

func readAdmissionChain(ctx context.Context, root *os.Root, member string) ([]CgroupSnapshot, error) {
	if !cleanAbsolute(member) || member == "/" {
		return nil, fmt.Errorf("invalid non-root membership: %w", ErrAdmission)
	}
	var chain []CgroupSnapshot
	for path := member; path != "/"; path = filepath.Dir(path) {
		if len(chain) == maxAdmissionDepth {
			return chain, fmt.Errorf("cgroup hierarchy depth: %w", ErrAdmission)
		}
		directory, err := root.OpenRoot(strings.TrimPrefix(path, "/"))
		if err != nil {
			return chain, fmt.Errorf("open cgroup %s: %w", path, err)
		}
		snapshot, readErr := readCgroupSnapshot(ctx, directory)
		if joinedErr := errors.Join(readErr, directory.Close()); joinedErr != nil {
			return chain, fmt.Errorf("read cgroup %s: %w", path, joinedErr)
		}
		snapshot.MemberPath = path
		chain = append(chain, *snapshot)
	}
	return chain, nil
}

func validateReferenceResources(environment Environment, chain []CgroupSnapshot, tasks uint64) error {
	r := environment.Runtime
	if r.Version != "go1.27.3" || r.GOOS != "linux" || r.GOMAXPROCS != referenceCPUs ||
		r.GCPercent != defaultGCPercentage || environment.Process.ObserverCPUAffinity < referenceCPUs || len(chain) == 0 {
		return fmt.Errorf("compiler, GOMAXPROCS, GC policy, affinity or ancestors: %w", ErrAdmission)
	}
	return validateReferenceLimits(chain, tasks)
}

func validateReferenceLimits(chain []CgroupSnapshot, tasks uint64) error {
	leaf := chain[0]
	if leaf.CPUQuotaUS.Unlimited || leaf.CPUPeriodUS == 0 || leaf.CPUQuotaUS.Value/leaf.CPUPeriodUS != referenceCPUs ||
		leaf.CPUQuotaUS.Value%leaf.CPUPeriodUS != 0 || leaf.MemoryMaxBytes.Unlimited ||
		leaf.MemoryMaxBytes.Value != referenceMemory {
		return fmt.Errorf("reference child requires exactly 8 CPU and 8 GiB limits: %w", ErrAdmission)
	}
	expectedPath := leaf.MemberPath
	for _, entry := range chain {
		if !cleanAbsolute(entry.MemberPath) || entry.MemberPath == "/" ||
			entry.MemberPath != expectedPath || !referenceAncestorFits(entry) {
			return fmt.Errorf("missing or tighter cgroup ancestor %s: %w", entry.MemberPath, ErrAdmission)
		}
		if !referenceTasksFit(entry, tasks) {
			return fmt.Errorf("cgroup %s tasks current=%d additional=%d max=%d: %w",
				entry.MemberPath, entry.PidsCurrent, tasks, entry.PidsMax.Value, ErrAdmission)
		}
		expectedPath = filepath.Dir(entry.MemberPath)
	}
	if expectedPath != "/" {
		return fmt.Errorf("incomplete cgroup ancestor chain: %w", ErrAdmission)
	}
	return nil
}

func referenceTasksFit(entry CgroupSnapshot, tasks uint64) bool {
	// The pinned baseline needs one thread per session; the margin is not a proven peak or reservation.
	return entry.PidsMax.Unlimited || (entry.PidsCurrent <= entry.PidsMax.Value &&
		tasks <= entry.PidsMax.Value-entry.PidsCurrent)
}

func referenceAncestorFits(entry CgroupSnapshot) bool {
	return entry.CPUPeriodUS != 0 && (entry.CPUQuotaUS.Unlimited ||
		entry.CPUQuotaUS.Value/entry.CPUPeriodUS >= referenceCPUs) &&
		(entry.MemoryMaxBytes.Unlimited || entry.MemoryMaxBytes.Value >= referenceMemory) &&
		(entry.MemoryHighBytes.Unlimited || entry.MemoryHighBytes.Value >= referenceMemory)
}
