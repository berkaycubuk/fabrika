package model

import "time"

// ActiveRun records the OS process-group id (pgid) of an in-flight agent run so
// a later boot can reap orphaned process groups left by a crash or kill. It
// lives in a dedicated runtime table because Attempts are only written at run
// END and so cannot carry a run-start pgid. StartedAt may be left zero when not
// selected; callers require only TaskID, PGID, and AgentID.
type ActiveRun struct {
	TaskID    string    `json:"taskId"`
	PGID      int       `json:"pgid"`
	AgentID   string    `json:"agentId"`
	StartedAt time.Time `json:"startedAt"`
}
