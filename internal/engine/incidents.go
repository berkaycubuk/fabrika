package engine

import (
	"github.com/berkaycubuk/fabrika/internal/model"
)

// ListIncidents returns all incidents when status is empty, or only those
// matching status when non-empty.
func (e *Engine) ListIncidents(status string) ([]model.Incident, error) {
	if status == "" {
		return e.store.Incidents.List()
	}
	return e.store.Incidents.ListByStatus(status)
}

// IgnoreIncident marks an incident as ignored and emits incident.updated.
// Returns store.ErrNotFound when no incident with the given id exists.
func (e *Engine) IgnoreIncident(id string) error {
	inc, err := e.store.Incidents.Get(id)
	if err != nil {
		return err
	}
	inc.Status = model.IncidentIgnored
	if err := e.store.Incidents.Update(inc); err != nil {
		return err
	}
	e.emit("incident.updated", *inc)
	return nil
}

// ResolveIncident marks an incident as resolved and emits incident.updated.
// Returns store.ErrNotFound when no incident with the given id exists.
func (e *Engine) ResolveIncident(id string) error {
	inc, err := e.store.Incidents.Get(id)
	if err != nil {
		return err
	}
	inc.Status = model.IncidentResolved
	if err := e.store.Incidents.Update(inc); err != nil {
		return err
	}
	e.emit("incident.updated", *inc)
	return nil
}
