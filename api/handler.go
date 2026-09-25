package api

import (
	"io/fs"
	"telecloud/config"
)

type Handler struct {
	cfg           *config.Config
	contentFS     fs.FS
	startTG       func(cfg *config.Config)
	restartApp    func()
	updateChecker *UpdateChecker
}

func NewHandler(cfg *config.Config, contentFS fs.FS, startTG func(cfg *config.Config), restartApp func()) *Handler {
	return &Handler{
		cfg:           cfg,
		contentFS:     contentFS,
		startTG:       startTG,
		restartApp:    restartApp,
		updateChecker: NewUpdateChecker(0),
	}
}
