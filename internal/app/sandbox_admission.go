package app

import (
	"errors"

	"gorm.io/gorm"
	"wave-ai.local/wave/internal/adapters/sandbox"
	"wave-ai.local/wave/internal/modules/environments"
	"wave-ai.local/wave/internal/modules/execution"
)

func (a *App) admitSandbox(tx *gorm.DB, session execution.Session, task *execution.Task) (bool, error) {
	if session.EnvironmentID == "" {
		return true, nil
	}
	env, err := environments.Get(tx.Statement.Context, tx, principal(session), session.EnvironmentID)
	if err != nil {
		return false, err
	}
	if router, ok := a.Sandbox.(*sandbox.Router); ok {
		err = router.ReserveSession(tx, session.ID, env.SandboxBackend, env.SandboxProfile)
	} else if box, ok := a.Sandbox.(sandbox.Managed); ok {
		resources, e := box.Resources(env.SandboxProfile)
		if e != nil {
			return false, e
		}
		err = box.Reserve(tx, session.ID, resources)
	} else {
		return true, nil
	}
	if errors.Is(err, sandbox.ErrCapacity) {
		return false, nil
	}
	if errors.Is(err, sandbox.ErrResources) {
		// An impossible request must fail visibly rather than queue forever.
		task.PendingFinish, task.Error = "failed", err.Error()
		return true, nil
	}
	return err == nil, err
}
