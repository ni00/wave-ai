package sandbox

import "time"

// Record persists only backend identity; execution owns business state.
type Record struct {
	SessionID  string `gorm:"primaryKey"`
	Backend    string `gorm:"not null"`
	EngineHost string
	Runtime    string
	Network    string
	StagerID   string
	BackendID  string
	Name       string
	State      string
	CPUs       uint32 `gorm:"column:cpus;not null"`
	MemoryMiB  uint64 `gorm:"column:memory_mib;not null"`
	// WorkspaceGiB is reserved at admission: 0 keeps /workspace on the
	// writable layer; a positive value caps it on a size-limited volume.
	WorkspaceGiB int
	Image        string
	UpdatedAt    time.Time
}

func (Record) TableName() string { return "sandbox_instances" }
