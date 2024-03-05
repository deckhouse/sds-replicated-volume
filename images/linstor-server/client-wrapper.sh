#!/usr/bin/bash

# Copyright 2023 Flant JSC
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

# only allowed subcommands of linstor client
valid_subcommands_list=("storage-pool" "sp" "node" "n" "resource" "r" "volume" "v" "resource-definition" "rd" "error-reports" "err")
valid_subcommands_ver=("controller" "c")
valid_subcommands_lv=("resource" "r")
valid_subcommands_advise=("advise" "adv")
valid_keys=("-m" "--output-version=v1")
allowed=false

if [[ -z "$1" || "--help" == "$1" || "-h" == "$1" ]]; then
  echo "This wrapper is designed to minimize manual intervention in the module's operation. We are continuously improving the built-in capabilities to avoid the need for console commands. Please try to use them as much as possible. The allowed commands at the moment are:"
  echo "- storage-pool list/show"
  echo "- node list/show"
  echo "- resource list/show/lv"
  echo "- volume list/show"
  echo "- resource-definition list/show"
  echo "- error-reports list/show"
  echo "- controller version"
  echo "- advise resource/maintainance"
  echo "- resource-definition delete"
  exit 0
fi

originalKeys=("$@")
checkKeys=true

while [[ $checkKeys == true ]]; do
  if [[ $(echo "${valid_keys[@]}" | fgrep -w -- $1) ]]; then
    shift
  else
    checkKeys=false
  fi
done

# Temporarily allow linstor node lost
 if [[ "$1" == "node" ]] && [[ "$2" == "lost" ]]; then
   allowed=true
 fi

# Allow linstor resource-definition delete
if [[ "$1" == "resource-definition" ]] && [[ "$2" == "delete" ]]; then
  allowed=true
fi

# check for allowed linstor ... l and linstor ... list commands
if [[ $(echo "${valid_subcommands_list[@]}" | fgrep -w -- $1) ]]; then
  if [[ "$2" == "l" || "$2" == "list" ]]; then
    allowed=true
  fi

  if [[ "$2" == "s" || "$2" == "show" ]]; then
    allowed=true
  fi

  if [[ "$2" == "set-property" ]] && [[ "$4" == "AutoplaceTarget" ]]; then
    allowed=true
  fi
fi


# check for allowed linstor ... v and linstor ... version commands
if [[ $(echo "${valid_subcommands_ver[@]}" | fgrep -w -- $1) ]]; then
  if [[ "$2" == "v" || "$2" == "version" ]]; then
    allowed=true
  fi
fi

# check for allowed linstor ... lv commands
if [[ $(echo "${valid_subcommands_lv[@]}" | fgrep -w -- $1) ]]; then
  if [[ "$2" == "lv" ]]; then
    allowed=true
  fi
fi

# check for allowed linstor advise commands
if [[ $(echo "${valid_subcommands_advise[@]}" | fgrep -w -- $1) ]]; then
  if [[ "$2" == "r" || "$2" == "resource" || "$2" == "m" || "$2" == "maintenance" ]]; then
    allowed=true
  fi
fi

if [[ $allowed == true ]]; then
  /usr/bin/originallinstor "${originalKeys[@]}"
else
  echo "You're not allowed to change state of linstor cluster manually. Please contact tech support"
  exit 1
fi
