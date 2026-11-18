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

package drbdkmsg

import (
	"strconv"
	"strings"
	"testing"
)

var kmsgRecordSeeds = []string{
	"",
	"6,1,2,-;drbd sdsrv-foo: role( Secondary -> Primary )",
	"6,1,2,-;drbd sdsrv-foo/0 drbd1: disk( Inconsistent -> UpToDate )",
	"6,1,2,-;drbd sdsrv-foo peer-a: conn( Connecting -> Connected )",
	"6,1,2,-;drbd sdsrv-foo/0 drbd1 peer-a: pdsk( DUnknown -> UpToDate )",
	"6,1,2,-;drbd /unregistered/sdsrv-foo: role( Primary -> Secondary )",
	"6,1,2,-;drbd /unregistered//unregistered/sdsrv-foo: x",
	"6,1,2,-;drbd: module loaded",
	"6,1,2,-;kernel: hello",
	"3,1234,5678901,-;drbd sdsrv-foo peer-a: Split-Brain detected, dropping connection",
	"191,1,2,-;drbd sdsrv-foo: x",
	"1000,1,2,-;drbd sdsrv-foo: x",
	",1,2,-;drbd sdsrv-foo: x",
	"4;drbd sdsrv-foo: x",
	"-4,1,2,-;drbd sdsrv-foo: x",
	"6,1,2,-;drbd sdsrv-foo",
	"6,1,2,-;drbd  sdsrv-foo: x",
	"6,1,2,-;drbd ;drbd sdsrv-foo: x",
}

func modelKmsgSeverity(line string) int {
	head, _, ok := strings.Cut(line, ",")
	if !ok || len(head) == 0 || len(head) > 3 {
		return -1
	}
	if strings.Trim(head, "0123456789") != "" {
		return -1
	}
	pri, err := strconv.Atoi(head)
	if err != nil {
		return -1
	}
	return pri % 8
}

func FuzzParseSeverity(f *testing.F) {
	for _, seed := range kmsgRecordSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, line []byte) {
		got := parseSeverity(line)
		if got < -1 || got > 7 {
			t.Fatalf("parseSeverity(%q) = %d, outside -1..7", line, got)
		}
		if want := modelKmsgSeverity(string(line)); got != want {
			t.Fatalf("parseSeverity(%q) = %d, want %d", line, got, want)
		}
	})
}

func FuzzParseResourceName(f *testing.F) {
	for _, seed := range kmsgRecordSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, line []byte) {
		name, ok := parseResourceName(line)

		_, rest, found := strings.Cut(string(line), ";drbd ")
		rest = strings.TrimPrefix(rest, "/unregistered/")
		want, _, _ := strings.Cut(rest, " ")
		want, _, _ = strings.Cut(want, "/")
		want, _, _ = strings.Cut(want, ":")
		wantOK := found && want != "" && len(want) < len(rest)
		if !wantOK {
			want = ""
		}
		if name != want || ok != wantOK {
			t.Fatalf("parseResourceName(%q) = %q, %v; want %q, %v", line, name, ok, want, wantOK)
		}

		if !ok {
			return
		}
		for _, tail := range []string{": role( Secondary -> Primary )", "/0 drbd1: x", " peer-a: x"} {
			record := "6,1,2,-;drbd " + name + tail
			if got, ok := parseResourceName([]byte(record)); !ok || got != name {
				t.Fatalf("parseResourceName(%q) = %q, %v; want %q, true", record, got, ok, name)
			}
			record = "6,1,2,-;drbd /unregistered/" + name + tail
			if got, ok := parseResourceName([]byte(record)); !ok || got != name {
				t.Fatalf("parseResourceName(%q) = %q, %v; want %q, true", record, got, ok, name)
			}
		}
	})
}
