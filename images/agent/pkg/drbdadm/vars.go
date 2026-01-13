/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package drbdadm

var Command = "drbdadm"

var DumpMDArgs = func(resource string) []string {
	return []string{"dump-md", resource}
}

var StatusArgs = func(resource string) []string {
	return []string{"status", resource}
}

var UpArgs = func(resource string) []string {
	return []string{"up", resource}
}

var AdjustArgs = func(resource string) []string {
	return []string{"adjust", resource}
}

var ShNopArgs = func(configToTest string, configToExclude string) []string {
	return []string{"--config-to-test", configToTest, "--config-to-exclude", configToExclude, "sh-nop"}
}

var CreateMDArgs = func(resource string) []string {
	return []string{"create-md", "--max-peers=7", "--force", resource}
}

var DownArgs = func(resource string) []string {
	return []string{"down", resource}
}

var PrimaryArgs = func(resource string) []string {
	return []string{"primary", resource}
}

var PrimaryForceArgs = func(resource string) []string {
	return []string{"primary", "--force", resource}
}

var SecondaryArgs = func(resource string) []string {
	return []string{"secondary", resource}
}

var Events2Args = []string{"events2", "--timestamps"}

var ResizeArgs = func(resource string) []string {
	return []string{"resize", resource}
}
