package drbdconfig

import "path/filepath"

var ControllerName = "drbd_config_controller"

var ResourcesDir = "/var/lib/sds-replicated-volume-agent.d/"

func filePaths(rvName string) (regularFilePath, tempFilePath string) {
	regularFilePath = filepath.Join(ResourcesDir, rvName+".res")
	tempFilePath = regularFilePath + "_tmp"
	return
}
