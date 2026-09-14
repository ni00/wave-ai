package sandbox

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCapacity = errors.New("sandbox capacity is full")
var ErrResources = errors.New("sandbox resources do not fit the configured budget")

// Resources is the guest allocation, not a measurement of host RSS.
type Resources struct {
	CPUs      uint32 `json:"cpus"`
	MemoryMiB uint64 `json:"memory_mib"`
}

func (s *Sbx) Resources(profile string) (Resources, error) {
	return profileResources(profile, Resources{s.opts.CPUs, s.opts.MemoryMiB})
}

func profileResources(profile string, defaults Resources) (Resources, error) {
	switch profile {
	case "", "default":
		return defaults, nil
	case "standard":
		return Resources{2, 2048}, nil
	case "large":
		return Resources{2, 4096}, nil
	default:
		return Resources{}, fmt.Errorf("unknown sandbox profile %q", profile)
	}
}

// Reserve must run inside a transaction. A DB-wide lock makes admission shared
// across workers/replicas. Reservations survive restarts and unknown outcomes;
// only a confirmed stop releases them. All replicas must use the same limits.
// Retained disks keep their original specification, including after a restart.
func (s *Sbx) Reserve(tx *gorm.DB, sessionID string, resources Resources) error {
	return reserve(tx, sessionID, resources, allocation{Backend: "sbx", Host: s.opts.BaseURL, Image: s.opts.Image, MaxRunning: s.opts.MaxRunning, MemoryBudgetMiB: s.opts.MemoryBudgetMiB})
}

type allocation struct {
	Backend, Host, Image, Runtime, Network string
	MaxRunning                             int
	MemoryBudgetMiB                        uint64
}

// Managed extends Provider with shared, transactional capacity admission.
type Managed interface {
	Provider
	Resources(string) (Resources, error)
	Reserve(*gorm.DB, string, Resources) error
}

func reserve(tx *gorm.DB, sessionID string, resources Resources, policy allocation) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?, ?)", 1463899717, 1).Error; err != nil {
		return err
	}
	var record Record
	err := tx.Where(clause.Eq{Column: "session_id", Value: sessionID}).Take(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	exists := err == nil
	if exists {
		if record.Backend != policy.Backend {
			return fmt.Errorf("session sandbox belongs to %s, not %s", record.Backend, policy.Backend)
		}
		if record.EngineHost != policy.Host {
			return errors.New("session sandbox belongs to a different engine endpoint; restore that endpoint or use a new session")
		}
		if record.CPUs == 0 || record.MemoryMiB == 0 || record.Image == "" {
			return errors.New("sandbox record has no resource specification")
		}
		if record.State != "stopped" {
			return nil
		}
		resources = Resources{record.CPUs, record.MemoryMiB}
	}
	if resources.CPUs == 0 || resources.MemoryMiB == 0 || resources.MemoryMiB > policy.MemoryBudgetMiB {
		return fmt.Errorf("%w: requested %d MiB, budget %d MiB", ErrResources, resources.MemoryMiB, policy.MemoryBudgetMiB)
	}
	var used struct {
		Count  int64
		Memory uint64
	}
	if err := tx.Model(&Record{}).Select("count(*) AS count, coalesce(sum(memory_mib), 0) AS memory").Where(clause.Neq{Column: "state", Value: "stopped"}).Scan(&used).Error; err != nil {
		return err
	}
	if used.Count >= int64(policy.MaxRunning) || used.Memory > policy.MemoryBudgetMiB-resources.MemoryMiB {
		return ErrCapacity
	}
	if exists {
		return tx.Model(&record).Update("state", "reserved").Error
	}
	return tx.Create(&Record{SessionID: sessionID, Name: "wave-" + sessionID, State: "reserved", CPUs: resources.CPUs, MemoryMiB: resources.MemoryMiB, Image: policy.Image, Backend: policy.Backend, EngineHost: policy.Host, Runtime: policy.Runtime, Network: policy.Network}).Error
}
