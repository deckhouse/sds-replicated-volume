package utils

import "log/slog"

func LogError(log *slog.Logger, err error) error {
	log.Error(err.Error())
	return err
}
