/*
Copyright 2026 Flant JSC

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

package dmsetup

import (
	"fmt"
	"strings"
	"testing"
)

const fuzzDmsetupInfoNoise = "Tables present:    LIVE & INACTIVE\n\n  Major, minor:      253, 7  \nno colon on this line\n"

func FuzzParseInfo(f *testing.F) {
	for _, seed := range []string{
		"",
		`Name:              vg--1-lv--1
State:             ACTIVE
Read Ahead:        256
Tables present:    LIVE
Open count:        0
Event number:      0
Major, minor:      253, 0
Number of targets: 1
UUID: LVM-JnCFQZTTfEEd2CnKoHyzPH4fMACKwIv7BmuLLu9ZSfeqpfqR3H23rQfIl7Ouyl5X
`,
		`Name:              sdsrv-foo-wipe
State:             SUSPENDED
Read Ahead:        256
Tables present:    LIVE
Open count:        2
Event number:      0
Major, minor:      253, 12
Number of targets: 1
`,
		"Name: a\r\nState: ACTIVE\r\nOpen count: 1\r\n",
		"Open count: -1\n",
		"Open count: +3\n",
		"Open count: x\n",
		"Open count:\n",
		"Name: a\nName: b\nOpen count: 1\nOpen count: 2\n",
		"Name:: with: colons\n",
		"  Open count  :  7  \n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, out string) {
		info, err := parseInfo(out)

		noisy := fuzzDmsetupInfoNoise + strings.ReplaceAll(out, "\n", "\n"+fuzzDmsetupInfoNoise) + "\n" + fuzzDmsetupInfoNoise
		noisyInfo, noisyErr := parseInfo(noisy)
		if (err == nil) != (noisyErr == nil) {
			t.Fatalf("unrelated lines changed the outcome: %v without them, %v with them", err, noisyErr)
		}
		if err != nil {
			if info != nil {
				t.Fatalf("parseInfo returned %+v together with the error %v", info, err)
			}
			return
		}
		if info == nil {
			t.Fatal("parseInfo returned neither info nor an error")
		}
		if *info != *noisyInfo {
			t.Fatalf("unrelated lines changed the result: %+v without them, %+v with them", *info, *noisyInfo)
		}

		for _, v := range []string{info.Name, info.State} {
			if strings.Contains(v, "\n") || strings.TrimSpace(v) != v {
				t.Fatalf("parsed the value %q, which is not one trimmed line of %q", v, out)
			}
		}

		rendered := fmt.Sprintf("Name: %s\nState: %s\nOpen count: %d\n", info.Name, info.State, info.OpenCount)
		again, err := parseInfo(rendered)
		if err != nil {
			t.Fatalf("the info %+v, rendered back as %q, does not parse: %v", *info, rendered, err)
		}
		if *again != *info {
			t.Fatalf("the info changed on a round trip: %+v, then %+v", *info, *again)
		}
	})
}
