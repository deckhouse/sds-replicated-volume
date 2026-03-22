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

package debug

import (
	"strconv"
	"strings"
)

// k8sProtobufMagic is the 4-byte magic header for Kubernetes protobuf encoding.
const k8sProtobufMagic = "k8s\x00"

// IsHexDump reports whether s looks like the output of encoding/hex.Dump
// (e.g. "00000000  6b 38 73 00 ...  |k8s...|").
func IsHexDump(s string) bool {
	if len(s) < 10 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return s[8] == ' ' && s[9] == ' '
}

// ParseHexDump extracts raw bytes from encoding/hex.Dump formatted output.
func ParseHexDump(s string) []byte {
	var result []byte
	for _, line := range strings.Split(s, "\n") {
		if len(line) < 10 || line[8] != ' ' || line[9] != ' ' {
			continue
		}
		hexPart := line[10:]
		if pipe := strings.IndexByte(hexPart, '|'); pipe >= 0 {
			hexPart = hexPart[:pipe]
		}
		for _, token := range strings.Fields(hexPart) {
			if len(token) != 2 {
				continue
			}
			b, err := strconv.ParseUint(token, 16, 8)
			if err != nil {
				continue
			}
			result = append(result, byte(b))
		}
	}
	return result
}

// ExtractK8sProtobufMeta extracts apiVersion and kind from Kubernetes
// protobuf-encoded bytes. The wire format is:
//
//	magic("k8s\0") + runtime.Unknown{ field1: TypeMeta{ field1: apiVersion, field2: kind }, field2: Raw, ... }
func ExtractK8sProtobufMeta(data []byte) (apiVersion, kind string) {
	if len(data) < 4 || string(data[:4]) != k8sProtobufMagic {
		return "", ""
	}
	data = data[4:]

	for len(data) > 0 {
		tag, n := protoDecodeVarint(data)
		if n == 0 {
			return
		}
		data = data[n:]
		fieldNum := tag >> 3
		wireType := tag & 0x7

		switch wireType {
		case 2: // length-delimited
			length, ln := protoDecodeVarint(data)
			if ln == 0 || int(length) > len(data[ln:]) {
				return
			}
			data = data[ln:]
			if fieldNum == 1 {
				return parseProtoTypeMeta(data[:length])
			}
			data = data[length:]
		case 0: // varint
			_, vn := protoDecodeVarint(data)
			if vn == 0 {
				return
			}
			data = data[vn:]
		case 1: // 64-bit
			if len(data) < 8 {
				return
			}
			data = data[8:]
		case 5: // 32-bit
			if len(data) < 4 {
				return
			}
			data = data[4:]
		default:
			return
		}
	}
	return
}

// parseProtoTypeMeta decodes the TypeMeta protobuf message:
// field 1 = apiVersion (string), field 2 = kind (string).
func parseProtoTypeMeta(data []byte) (apiVersion, kind string) {
	for len(data) > 0 {
		tag, n := protoDecodeVarint(data)
		if n == 0 {
			return
		}
		data = data[n:]
		fieldNum := tag >> 3
		wireType := tag & 0x7

		if wireType != 2 {
			switch wireType {
			case 0:
				_, vn := protoDecodeVarint(data)
				if vn == 0 {
					return
				}
				data = data[vn:]
			case 1:
				if len(data) < 8 {
					return
				}
				data = data[8:]
			case 5:
				if len(data) < 4 {
					return
				}
				data = data[4:]
			default:
				return
			}
			continue
		}

		length, ln := protoDecodeVarint(data)
		if ln == 0 || int(length) > len(data[ln:]) {
			return
		}
		data = data[ln:]
		switch fieldNum {
		case 1:
			apiVersion = string(data[:length])
		case 2:
			kind = string(data[:length])
		}
		data = data[length:]
	}
	return
}

// protoDecodeVarint decodes a protobuf varint from the beginning of data.
// Returns the value and the number of bytes consumed (0 if truncated).
func protoDecodeVarint(data []byte) (uint64, int) {
	var value uint64
	for i, b := range data {
		if i >= 10 {
			return 0, 0
		}
		value |= uint64(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			return value, i + 1
		}
	}
	return 0, 0
}
