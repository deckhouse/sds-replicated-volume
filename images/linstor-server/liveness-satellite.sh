#!/bin/sh

# Copyright 2024 Flant JSC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Sometimes nodes can be shown as Online without established connection to them.
# This is a workaround for https://github.com/LINBIT/linstor-server/issues/331, https://github.com/LINBIT/linstor-server/issues/219

# Check if there are symptoms of lost connection in linstor controller logs
if test -f "/var/log/linstor-satellite/linstor-Satellite.log"; then
  if [ $(tail -n 1000 /var/log/linstor-satellite/linstor-Satellite.log | grep 'Target decrypted buffer is too small' | wc -l) -ne 0 ]; then
    exit 1
  fi
fi

# Because shell keeps last exit code, we must force exit with code 0. If not, we will have exit code 1 because of grep, that not founded anything
exit 0
