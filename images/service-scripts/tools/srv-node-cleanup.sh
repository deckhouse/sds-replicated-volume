#!/usr/bin/env bash
# Copyright 2026 Flant JSC
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

set -Eeuo pipefail

export LVM_SUPPRESS_FD_WARNINGS=1
ROOT="${SRV_NODE_CLEANUP_ROOT:-}"
APPLY=0
DELETE_VOLUMES=0
FORCE=0
VGS=()
FAILURES=0
REBOOT_NEEDED=0
PREFLIGHT_FAILED=0
TS="$(date +%Y%m%d-%H%M%S)"
LOG_FILE="${ROOT}/var/log/srv-node-cleanup-${TS}.log"

LINSTOR_LV_RE='^pvc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}_[0-9]{5}$'
NEW_CP_LV_RE='^pvc-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-[0-9]{1,2}-[0-9a-f]{32}$'
DRBD_FILTER_ELEMENT='"r|^/dev/drbd|"'

usage() {
  cat <<'EOF'
Usage: srv-node-cleanup.sh [--apply] [--delete-volumes --vg <name> ...] [--force]

Removes DRBD and sds-replicated-volume leftovers from a node that is no longer
managed by a cluster. Without --apply it only prints what it would do.

  --apply            perform the actions; without it nothing is changed
  --delete-volumes   also remove the module's logical volumes (LINSTOR
                     "pvc-<uuid>_NNNNN" and new control plane
                     "pvc-<uuid>-N-<hash>") from the volume groups given by --vg
  --vg <name>        volume group to look for the module's logical volumes in;
                     may be repeated; volume groups, physical volumes and thin
                     pools are never removed
  --force            bring down DRBD resources that are Primary
  -h, --help         show this help
EOF
}

die() {
  echo "ERROR: $*" >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply) APPLY=1 ;;
    --delete-volumes) DELETE_VOLUMES=1 ;;
    --force) FORCE=1 ;;
    --vg)
      [[ $# -ge 2 ]] || die "--vg requires a volume group name"
      VGS+=("$2")
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown argument: $1"
      ;;
  esac
  shift
done

if [[ ${DELETE_VOLUMES} -eq 1 && ${#VGS[@]} -eq 0 ]]; then
  die "--delete-volumes requires at least one --vg"
fi
if [[ ${#VGS[@]} -gt 0 && ${DELETE_VOLUMES} -eq 0 ]]; then
  echo "NOTE: --vg without --delete-volumes only lists the volumes that would be removed"
fi

if [[ ${EUID} -ne 0 && -z "${ROOT}" ]]; then
  die "must be run as root"
fi

if [[ ${APPLY} -eq 1 ]]; then
  mkdir -p "$(dirname "${LOG_FILE}")"
  exec > >(tee -a "${LOG_FILE}") 2>&1
  echo "Log file: ${LOG_FILE}"
  MODE_LABEL="DOING"
else
  echo "Dry run: nothing is changed. Pass --apply to perform the actions."
  MODE_LABEL="WOULD"
fi

have() {
  command -v "$1" >/dev/null 2>&1
}

act() {
  local description="$1"
  shift
  echo "${MODE_LABEL}  ${description}"
  if [[ ${APPLY} -eq 1 ]]; then
    if ! "$@"; then
      echo "FAILED  ${description}" >&2
      FAILURES=$((FAILURES + 1))
      return 1
    fi
  fi
  return 0
}

preflight_fail() {
  echo "PREFLIGHT  $*" >&2
  PREFLIGHT_FAILED=1
}

drbd_resources() {
  have drbdsetup || return 0
  drbdsetup status 2>/dev/null | awk '/^[^[:space:]]/ { print $1 }' || true
}

primary_resources() {
  have drbdsetup || return 0
  drbdsetup status 2>/dev/null | awk '/^[^[:space:]]+[[:space:]]+role:Primary/ { print $1 }' || true
}

drbd_devices_present() {
  local dev
  for dev in "${ROOT}"/sys/block/drbd*; do
    if [[ -e "${dev}" ]]; then
      return 0
    fi
  done
  return 1
}

module_loaded() {
  grep -q "^$1 " "${ROOT}/proc/modules" 2>/dev/null
}

check_preflight() {
  echo "== Preflight"
  if have systemctl && systemctl is-active --quiet kubelet 2>/dev/null; then
    preflight_fail "kubelet is active: stop it (systemctl disable --now bashible.timer kubelet) once the node is out of the cluster"
  fi

  local kubeconfig="${ROOT}/etc/kubernetes/kubelet.conf"
  local kubectl_bin="${ROOT}/opt/deckhouse/bin/kubectl"
  if [[ -f "${kubeconfig}" && -x "${kubectl_bin}" ]]; then
    local node answer
    node="$(hostname | tr '[:upper:]' '[:lower:]')"
    if answer="$(timeout 15 "${kubectl_bin}" --kubeconfig "${kubeconfig}" get node "${node}" -o name 2>&1)"; then
      preflight_fail "node ${node} is still registered in a cluster: use evict.sh instead of this script"
    elif [[ "${answer}" == *NotFound* ]]; then
      echo "node ${node} is not registered in the cluster"
    else
      answer="${answer%%$'\n'*}"
      echo "WARNING: cannot verify from the node that it left the cluster (${answer:0:100}), make sure it did"
    fi
  else
    echo "no kubelet credentials on the node, cluster membership is not checked"
  fi

  local primaries
  primaries="$(primary_resources)"
  if [[ -n "${primaries}" && ${FORCE} -eq 0 ]]; then
    preflight_fail "DRBD resources in the Primary role: $(echo "${primaries}" | tr '\n' ' ')- stop what uses them or pass --force"
  fi

  if ! have drbdsetup && drbd_devices_present; then
    preflight_fail "DRBD devices exist but drbdsetup is not installed on the host: install drbd-utils or reboot the node with kubelet and bashible.timer disabled, then run the script again"
  fi

  if [[ ${PREFLIGHT_FAILED} -eq 1 && ${APPLY} -eq 1 ]]; then
    echo "Preflight failed, nothing was changed." >&2
    exit 1
  fi
  if [[ ${PREFLIGHT_FAILED} -eq 1 ]]; then
    echo "Preflight failed: the plan below is informational, --apply would stop here."
  fi
}

stop_bashible() {
  have systemctl || return 0
  local unit state
  for unit in bashible.timer bashible.service; do
    state="$(systemctl is-enabled "${unit}" 2>/dev/null || true)"
    if [[ "${state}" == enabled* ]]; then
      act "systemctl disable --now ${unit}" systemctl disable --now "${unit}" || true
    elif systemctl is-active --quiet "${unit}" 2>/dev/null; then
      act "systemctl stop ${unit}" systemctl stop "${unit}" || true
    fi
  done
}

stop_module_containers() {
  echo "== Containers of the module"
  local crictl_bin="${ROOT}/opt/deckhouse/bin/crictl"
  if [[ ! -x "${crictl_bin}" ]]; then
    crictl_bin="$(command -v crictl || true)"
  fi
  if [[ -z "${crictl_bin}" ]]; then
    echo "crictl is not installed, containers are not looked for"
    return 0
  fi
  local ids id found=0
  if ! ids="$("${crictl_bin}" ps -q --label io.kubernetes.pod.namespace=d8-sds-replicated-volume 2>/dev/null)"; then
    echo "WARNING: cannot list containers, make sure no container of the module is running"
    return 0
  fi
  while IFS= read -r id; do
    [[ -n "${id}" ]] || continue
    found=1
    act "crictl stop ${id}" "${crictl_bin}" stop "${id}" || true
  done <<<"${ids}"
  [[ ${found} -eq 1 ]] || echo "no running containers of the module"
}

bring_down_drbd() {
  echo "== DRBD resources"
  local resource found=0
  while IFS= read -r resource; do
    [[ -n "${resource}" ]] || continue
    found=1
    if [[ ${FORCE} -eq 1 ]]; then
      act "drbdsetup down --force ${resource}" drbdsetup down --force "${resource}" || true
    else
      act "drbdsetup down ${resource}" drbdsetup down "${resource}" || true
    fi
  done < <(drbd_resources)
  [[ ${found} -eq 1 ]] || echo "no DRBD resources"
}

drbd_module_files() {
  have modinfo || return 0
  local path version dir
  path="$(modinfo -n drbd 2>/dev/null || true)"
  [[ -f "${path}" ]] || return 0
  version="$(modinfo -F version drbd 2>/dev/null || true)"
  [[ "${version}" == 9* ]] || return 0
  dir="$(dirname "${path}")"
  for path in "${dir}"/drbd*.ko*; do
    if [[ -f "${path}" ]]; then
      echo "${path}"
    fi
  done
}

rmmod_retry() {
  local module="$1" attempts=10
  while [[ ${attempts} -gt 0 ]]; do
    if rmmod "${module}"; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "WARNING: module ${module} is still in use (refcnt $(cat "${ROOT}/sys/module/${module}/refcnt" 2>/dev/null || echo unknown)) and stays loaded until the node is rebooted"
  REBOOT_NEEDED=1
  return 0
}

unload_drbd_modules() {
  echo "== DRBD kernel modules"
  local module version=""
  if [[ -r "${ROOT}/sys/module/drbd/version" ]]; then
    version="$(cat "${ROOT}/sys/module/drbd/version")"
  fi
  if [[ -n "${version}" && "${version}" != 9* ]]; then
    echo "loaded drbd module is version ${version} (not installed by the module), left alone"
    return 0
  fi
  local any=0 transports
  transports="$(awk '/^drbd_transport_/ { print $1 }' "${ROOT}/proc/modules" 2>/dev/null || true)"
  for module in ${transports} drbd; do
    if module_loaded "${module}"; then
      any=1
      act "rmmod ${module}" rmmod_retry "${module}" || true
    fi
  done
  [[ ${any} -eq 1 ]] || echo "no DRBD modules loaded"
}

remove_path() {
  local path="$1"
  if [[ -e "${path}" || -L "${path}" ]]; then
    act "rm -rf ${path}" rm -rf "${path}" || true
  fi
}

remove_if_content() {
  local path="$1" expected="$2"
  [[ -f "${path}" ]] || return 0
  if [[ "$(grep -v '^[[:space:]]*$' "${path}")" == "${expected}" ]]; then
    act "rm ${path}" rm -f "${path}" || true
  else
    echo "SKIP  ${path} was modified, left alone"
  fi
}

remove_files() {
  echo "== Files and directories"
  remove_if_content "${ROOT}/etc/modprobe.d/drbd.conf" "options drbd usermode_helper=disabled"
  remove_if_content "${ROOT}/etc/modules-load.d/d8_drbd.conf" "drbd"

  if [[ -f "${ROOT}/etc/modules" ]] && grep -q -E '^drbd$' "${ROOT}/etc/modules"; then
    act "remove the drbd line from /etc/modules" sed -i '/^drbd$/d' "${ROOT}/etc/modules" || true
  fi

  local multipath_conf="${ROOT}/etc/multipath/conf.d/drbd.conf"
  if [[ -f "${multipath_conf}" ]]; then
    act "rm ${multipath_conf}" rm -f "${multipath_conf}" || true
    if have systemctl && systemctl is-enabled --quiet multipathd 2>/dev/null; then
      act "systemctl reload multipathd" systemctl reload multipathd || true
    fi
  fi

  remove_path "${ROOT}/opt/deckhouse/drbd"
  remove_path "${ROOT}/var/cache/registrypackages/drbd"
  remove_path "${ROOT}/var/cache/registrypackages/drbdLegacy"
  remove_path "${ROOT}/dev/sdsrv"
}

lvm_conf_rewrite() {
  awk -v element="${DRBD_FILTER_ELEMENT}" '
    function drop(line,    pre, post, i) {
      i = index(line, element)
      if (i == 0) return line
      pre = substr(line, 1, i - 1)
      post = substr(line, i + length(element))
      if (match(post, /^[[:space:]]*,[[:space:]]*/)) {
        post = substr(post, RLENGTH + 1)
      } else if (match(pre, /,[[:space:]]*$/)) {
        pre = substr(pre, 1, RSTART - 1)
      }
      return pre post
    }
    /^[[:space:]]*#/ { print; next }
    /^[[:space:]]*global_filter[[:space:]]*=[[:space:]]*\[.*\]/ {
      if (index($0, element) > 0) {
        line = drop($0)
        if (line ~ /=[[:space:]]*\[[[:space:]]*\]/) next
        print line
        next
      }
    }
    /^[[:space:]]*use_lvmetad[[:space:]]*=[[:space:]]*0[[:space:]]*(#.*)?$/ { next }
    { print }
  ' "$1"
}

edit_lvm_conf() {
  echo "== LVM configuration"
  local conf="${ROOT}/etc/lvm/lvm.conf"
  if [[ ! -f "${conf}" ]]; then
    echo "no ${conf}"
    return 0
  fi
  local tmp leftover=0
  tmp="$(mktemp "${conf}.XXXXXX")"
  lvm_conf_rewrite "${conf}" >"${tmp}"
  if cmp -s "${conf}" "${tmp}"; then
    echo "no DRBD filter or use_lvmetad=0 in ${conf}"
  else
    echo "${MODE_LABEL}  edit ${conf} (backup ${conf}.srv-cleanup.${TS}):"
    diff -u "${conf}" "${tmp}" | sed 's/^/    /' || true
    if [[ ${APPLY} -eq 1 ]]; then
      cp -a "${conf}" "${conf}.srv-cleanup.${TS}"
      cat "${tmp}" >"${conf}"
    fi
  fi
  if grep -v '^[[:space:]]*#' "${tmp}" | grep -qF "${DRBD_FILTER_ELEMENT}"; then
    leftover=1
  fi
  rm -f "${tmp}"
  if [[ ${leftover} -eq 1 ]]; then
    echo "MANUAL  ${DRBD_FILTER_ELEMENT} is still in ${conf} in a form the script does not rewrite (for example a multi-line global_filter): remove it by hand"
  fi
}

find_lvm() {
  local candidate
  if have lvm; then
    echo lvm
    return 0
  fi
  for candidate in \
    "${ROOT}/var/lib/deckhouse/sds/bin/lvm" \
    "${ROOT}/opt/deckhouse/sds/bin/lvm" \
    "${ROOT}/opt/deckhouse/sds/bin/lvm.static"; do
    if [[ -x "${candidate}" ]]; then
      echo "${candidate}"
      return 0
    fi
  done
}

remove_volumes() {
  [[ ${#VGS[@]} -gt 0 ]] || return 0
  echo "== Logical volumes"
  local lvm_bin vg lv matched
  lvm_bin="$(find_lvm)"
  if [[ -z "${lvm_bin}" ]]; then
    echo "WARNING: lvm is not found in PATH or in the sds-node-configurator directories, logical volumes are not looked for"
    return 0
  fi
  for vg in "${VGS[@]}"; do
    if ! "${lvm_bin}" vgs --noheadings -o vg_name "${vg}" >/dev/null 2>&1; then
      echo "volume group ${vg} not found"
      continue
    fi
    matched=0
    while IFS= read -r lv; do
      lv="${lv//[[:space:]]/}"
      [[ -n "${lv}" ]] || continue
      if [[ "${lv}" =~ ${LINSTOR_LV_RE} || "${lv}" =~ ${NEW_CP_LV_RE} ]]; then
        matched=1
        if [[ ${DELETE_VOLUMES} -eq 1 ]]; then
          act "lvremove ${vg}/${lv}" "${lvm_bin}" lvremove -y "${vg}/${lv}" || true
        else
          echo "FOUND  ${vg}/${lv} (kept, pass --delete-volumes to remove)"
        fi
      else
        echo "KEEP  ${vg}/${lv} (not a volume of the module)"
      fi
    done < <("${lvm_bin}" lvs --noheadings -o lv_name "${vg}" 2>/dev/null)
    [[ ${matched} -eq 1 ]] || echo "no volumes of the module in ${vg}"
  done
}

main() {
  check_preflight
  stop_bashible
  local module_files
  module_files="$(drbd_module_files)"
  stop_module_containers
  bring_down_drbd
  unload_drbd_modules
  remove_volumes
  remove_module_files_from "${module_files}"
  remove_files
  edit_lvm_conf

  echo "== Summary"
  if [[ ${FAILURES} -gt 0 ]]; then
    echo "${FAILURES} action(s) failed, see FAILED lines above" >&2
    exit 1
  fi
  if [[ ${PREFLIGHT_FAILED} -eq 1 ]]; then
    exit 1
  fi
  if [[ ${APPLY} -eq 1 ]]; then
    if [[ ${REBOOT_NEEDED} -eq 1 ]]; then
      echo "The drbd module stays loaded until the node is rebooted, which cleanup_static_node.sh does at the end: its files and its configuration are already removed, so it is not loaded again."
    fi
    echo "Done. Run cleanup_static_node.sh to finish the node cleanup."
  else
    echo "Dry run finished. Review the plan and run again with --apply."
  fi
}

remove_module_files_from() {
  local file removed=0
  while IFS= read -r file; do
    [[ -n "${file}" ]] || continue
    removed=1
    act "rm ${file}" rm -f "${file}" || true
  done <<<"$1"
  if [[ ${removed} -eq 1 ]]; then
    act "depmod" depmod || true
  fi
}

main
