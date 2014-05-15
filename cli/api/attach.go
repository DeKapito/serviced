package api

import (
	"github.com/zenoss/serviced/dao"
	zkdocker "github.com/zenoss/serviced/zzk/docker"
)

// AttachConfig is the deserialized object from the command-line
type AttachConfig struct {
	Running *dao.RunningService
	Command string
	Args    []string
}

func (a *api) GetRunningServices() ([]*dao.RunningService, error) {
	client, err := a.connectDAO()
	if err != nil {
		return nil, err
	}

	var rss []*dao.RunningService
	if err := client.GetRunningServices(&empty, &rss); err != nil {
		return nil, err
	}

	dc, err := a.connectDocker()
	if err != nil {
		return rss, err
	}

	for _, rs := range rss {
		container, err := dc.InspectContainer(rs.Id + "/")
		if err != nil {
			return rss, err
		}
		rs.DockerId = container.ID
	}

	return rss, nil
}

// Attach runs an arbitrary shell command in a running service container
func (a *api) Attach(config AttachConfig) error {
	client, err := a.connectDAO()
	if err != nil {
		return err
	}

	req := dao.AttachRequest{
		Running: config.Running,
		Command: config.Command,
		Args:    config.Args,
	}

	var res zkdocker.Attach
	if err := client.Attach(req, &res); err != nil {
		return err
	}

	return res.Error
}

// Action runs a predefined action in a running service container
func (a *api) Action(config AttachConfig) ([]byte, error) {
	client, err := a.connectDAO()
	if err != nil {
		return nil, err
	}

	req := dao.AttachRequest{
		Running: config.Running,
		Command: config.Command,
		Args:    config.Args,
	}

	var res zkdocker.Attach
	if err := client.Action(req, &res); err != nil {
		return nil, err
	}

	return res.Output, res.Error
}
