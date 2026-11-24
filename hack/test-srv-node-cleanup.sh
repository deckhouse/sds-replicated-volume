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

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${REPO}/images/service-scripts/tools/srv-node-cleanup.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/srv-node-cleanup-test.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

FAILED=0
check() {
  local name="$1"
  shift
  if "$@"; then
    echo "ok    ${name}"
  else
    echo "FAIL  ${name}"
    FAILED=1
  fi
}

ROOT_DIR="${WORK}/root"
BIN="${WORK}/bin"
STATE="${WORK}/state"
mkdir -p "${BIN}" "${STATE}"

UUID1="11111111-2222-3333-4444-555555555555"
UUID2="aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
HASH="0123456789abcdef0123456789abcdef"

make_root() {
  rm -rf "${ROOT_DIR}"
  mkdir -p "${ROOT_DIR}"/{etc/modprobe.d,etc/modules-load.d,etc/dracut.conf.d,etc/multipath/conf.d,etc/lvm,opt/deckhouse/drbd,var/cache/registrypackages/drbd,var/cache/registrypackages/semver,dev/sdsrv,proc,sys/module/drbd,var/log,lib/modules/6.1/updates}
  echo "options drbd usermode_helper=disabled" >"${ROOT_DIR}/etc/modprobe.d/drbd.conf"
  echo "drbd" >"${ROOT_DIR}/etc/modules-load.d/d8_drbd.conf"
  echo 'omit_drivers+=" drbd "' >"${ROOT_DIR}/etc/dracut.conf.d/d8_drbd.conf"
  printf 'blacklist {\n        devnode "^drbd[0-9]+"\n}\n' >"${ROOT_DIR}/etc/multipath/conf.d/drbd.conf"
  printf 'loop\ndrbd\nnbd\n' >"${ROOT_DIR}/etc/modules"
  cat >"${ROOT_DIR}/etc/lvm/lvm.conf" <<'CONF'
devices {
	# global_filter = [ "r|^/dev/drbd|" ]
	global_filter = [ "r|^/dev/drbd|", "r|^/dev/zd|", "a|.*|" ]
}
global {
	use_lvmetad = 0
	locking_type = 1
}
CONF
  touch "${ROOT_DIR}/lib/modules/6.1/updates/drbd.ko" "${ROOT_DIR}/lib/modules/6.1/updates/drbd_transport_tcp.ko" "${ROOT_DIR}/lib/modules/6.1/updates/drbd_transport_lb-tcp.ko"
  echo "9.2.14" >"${ROOT_DIR}/sys/module/drbd/version"
  printf 'drbd_transport_lb_tcp 1 1 - Live 0x0\ndrbd_transport_tcp 1 1 - Live 0x0\ndrbd 1 1 - Live 0x0\n' >"${ROOT_DIR}/proc/modules"
  printf '%s\n' \
    "pvc-${UUID1}_00000" "pvc-${UUID2}-1-${HASH}" "thinpool" "root" "pvc-${UUID1}_00000_snap" >"${STATE}/lvs"
  echo "drbd-res-1 role:Secondary" >"${STATE}/drbd"
  echo "c0ffee01" >"${STATE}/containers"
  touch "${STATE}/bashible_on"
  : >"${STATE}/calls"
}

write_mocks() {
  cat >"${BIN}/drbdsetup" <<MOCK
#!/usr/bin/env bash
echo "drbdsetup \$*" >>"${STATE}/calls"
if [[ "\$1" == status ]]; then cat "${STATE}/drbd"; fi
if [[ "\$1" == down ]]; then : >"${STATE}/drbd"; fi
MOCK
  cat >"${BIN}/modinfo" <<MOCK
#!/usr/bin/env bash
if [[ "\$1" == -n ]]; then echo "${ROOT_DIR}/lib/modules/6.1/updates/drbd.ko"; fi
if [[ "\$1" == -F ]]; then echo "9.2.14"; fi
MOCK
  cat >"${BIN}/rmmod" <<MOCK
#!/usr/bin/env bash
if [[ -f "${STATE}/rmmod_busy" ]]; then rm -f "${STATE}/rmmod_busy"; exit 1; fi
if [[ -f "${STATE}/rmmod_stuck" ]]; then exit 1; fi
echo "rmmod \$*" >>"${STATE}/calls"
sed -i "/^\$1 /d" "${ROOT_DIR}/proc/modules"
MOCK
  cat >"${BIN}/crictl" <<MOCK
#!/usr/bin/env bash
echo "crictl \$*" >>"${STATE}/calls"
if [[ "\$1" == ps ]]; then cat "${STATE}/containers"; fi
if [[ "\$1" == stop ]]; then : >"${STATE}/containers"; fi
MOCK
  cat >"${BIN}/depmod" <<MOCK
#!/usr/bin/env bash
echo "depmod" >>"${STATE}/calls"
MOCK
  cat >"${BIN}/lvm" <<MOCK
#!/usr/bin/env bash
sub="\$1"
shift
case "\$sub" in
  vgs) [[ "\${@: -1}" == vg0 ]] ;;
  lvs) sed 's/^/  /' "${STATE}/lvs" ;;
  lvremove)
    echo "lvremove \$*" >>"${STATE}/calls"
    target="\${@: -1}"
    sed -i "/^\${target#*/}\$/d" "${STATE}/lvs"
    ;;
esac
MOCK
  cat >"${BIN}/systemctl" <<MOCK
#!/usr/bin/env bash
echo "systemctl \$*" >>"${STATE}/calls"
case "\$1" in
  is-enabled)
    case "\${@: -1}" in
      bashible.timer) if [[ -f "${STATE}/bashible_on" ]]; then echo enabled; else echo disabled; exit 1; fi ;;
      bashible.service) echo static ;;
      *) echo disabled; exit 1 ;;
    esac
    ;;
  is-active) [[ "\${@: -1}" == bashible.timer && -f "${STATE}/bashible_on" ]] ;;
  disable) rm -f "${STATE}/bashible_on" ;;
  *) exit 0 ;;
esac
MOCK
  chmod +x "${BIN}"/*
}

run() {
  PATH="${BIN}:${PATH}" SRV_NODE_CLEANUP_ROOT="${ROOT_DIR}" bash "${SCRIPT}" "$@"
}

snapshot() {
  (cd "${ROOT_DIR}" && find . -type f -o -type d | sort && find . -type f -exec md5sum {} + | sort) | md5sum
  cat "${STATE}/lvs" "${STATE}/drbd" "${STATE}/containers" | md5sum
}

write_mocks

make_root
before="$(snapshot)"
run --delete-volumes --vg vg0 >"${WORK}/dry.out" 2>&1
check "dry run leaves the tree and the state unchanged" test "$(snapshot)" == "${before}"
check "dry run makes no mutating calls" test "$(grep -cE '^(rmmod|lvremove|depmod|drbdsetup down)' "${STATE}/calls")" -eq 0
check "dry run lists the DRBD resource" grep -q 'WOULD  drbdsetup down drbd-res-1' "${WORK}/dry.out"
check "dry run lists the LINSTOR volume" grep -q "WOULD  lvremove vg0/pvc-${UUID1}_00000\$" "${WORK}/dry.out"
check "dry run lists the new control plane volume" grep -q "WOULD  lvremove vg0/pvc-${UUID2}-1-${HASH}" "${WORK}/dry.out"
check "dry run keeps foreign volumes" grep -q 'KEEP  vg0/thinpool' "${WORK}/dry.out"
check "dry run keeps a snapshot-like name" grep -q "KEEP  vg0/pvc-${UUID1}_00000_snap" "${WORK}/dry.out"
check "dry run shows the lvm.conf diff" grep -q '^    -.*use_lvmetad' "${WORK}/dry.out"

make_root
run --vg vg0 >"${WORK}/novol.out" 2>&1
check "without --delete-volumes volumes are only found" grep -q "FOUND  vg0/pvc-${UUID1}_00000" "${WORK}/novol.out"

make_root
run --delete-volumes --vg vg0 --apply >"${WORK}/apply.out" 2>&1 || { cat "${WORK}/apply.out"; exit 1; }
check "apply removes the module volumes only" test "$(tr '\n' ' ' <"${STATE}/lvs")" == "thinpool root pvc-${UUID1}_00000_snap "
check "dry run plans to disable bashible.timer" grep -q 'WOULD  systemctl disable --now bashible.timer' "${WORK}/dry.out"
check "apply disables bashible.timer so a reboot does not bring it back" test ! -e "${STATE}/bashible_on"
check "apply removes the lb-tcp transport module file" test ! -e "${ROOT_DIR}/lib/modules/6.1/updates/drbd_transport_lb-tcp.ko"
check "apply stops the containers of the module" test ! -s "${STATE}/containers"
check "dry run does not stop containers" test "$(grep -c 'WOULD  crictl stop c0ffee01' "${WORK}/dry.out")" -eq 1
check "apply brings the resource down" test ! -s "${STATE}/drbd"
check "apply unloads the modules" test ! -s "${ROOT_DIR}/proc/modules"
check "apply removes module files" test ! -e "${ROOT_DIR}/lib/modules/6.1/updates/drbd.ko"
check "apply removes the configs" test ! -e "${ROOT_DIR}/etc/modprobe.d/drbd.conf" -a ! -e "${ROOT_DIR}/etc/modules-load.d/d8_drbd.conf" -a ! -e "${ROOT_DIR}/etc/dracut.conf.d/d8_drbd.conf" -a ! -e "${ROOT_DIR}/etc/multipath/conf.d/drbd.conf"
check "apply removes the sources, the package state and /dev/sdsrv" test ! -e "${ROOT_DIR}/opt/deckhouse/drbd" -a ! -e "${ROOT_DIR}/var/cache/registrypackages/drbd" -a ! -e "${ROOT_DIR}/dev/sdsrv"
check "apply keeps other registry packages" test -d "${ROOT_DIR}/var/cache/registrypackages/semver"
check "apply cleans /etc/modules" test "$(tr '\n' ' ' <"${ROOT_DIR}/etc/modules")" == "loop nbd "
check "apply rewrites global_filter and drops use_lvmetad" grep -qF 'global_filter = [ "r|^/dev/zd|", "a|.*|" ]' "${ROOT_DIR}/etc/lvm/lvm.conf"
check "apply keeps the commented line" grep -qF '# global_filter = [ "r|^/dev/drbd|" ]' "${ROOT_DIR}/etc/lvm/lvm.conf"
check "apply removes use_lvmetad" test "$(grep -c use_lvmetad "${ROOT_DIR}/etc/lvm/lvm.conf")" -eq 0
check "apply keeps other lvm.conf settings" grep -q 'locking_type = 1' "${ROOT_DIR}/etc/lvm/lvm.conf"
check "apply leaves a lvm.conf backup" test "$(find "${ROOT_DIR}/etc/lvm" -name "lvm.conf.srv-cleanup.*" | wc -l)" -eq 1
check "apply writes a log" test "$(find "${ROOT_DIR}/var/log" -name "srv-node-cleanup-*.log" | wc -l)" -eq 1
check "dry run plan equals the applied actions" diff <(grep -E '^WOULD' "${WORK}/dry.out" | sed 's/^WOULD  //' | grep -v '^  ' | grep -v -E '^edit ') <(grep -E '^DOING' "${WORK}/apply.out" | sed 's/^DOING  //' | grep -v -E '^edit ')

: >"${STATE}/calls"
run --delete-volumes --vg vg0 --apply >"${WORK}/again.out" 2>&1
check "second apply is a no-op" test "$(grep -cE '^(DOING|WOULD)' "${WORK}/again.out")" -eq 0
check "second apply exits successfully" true

make_root
echo "drbd-res-1 role:Primary" >"${STATE}/drbd"
set +e
run --apply >"${WORK}/primary.out" 2>&1
code=$?
set -e
check "Primary resource blocks apply" test "${code}" -eq 1
check "blocked apply changes nothing" test -e "${ROOT_DIR}/etc/modprobe.d/drbd.conf"
run --force --apply >"${WORK}/force.out" 2>&1
check "--force brings a Primary resource down" grep -q 'drbdsetup down --force drbd-res-1' "${STATE}/calls"

make_root
touch "${STATE}/rmmod_busy"
run --apply >"${WORK}/busy.out" 2>&1 || { cat "${WORK}/busy.out"; exit 1; }
check "a module that is busy for a moment is unloaded on retry" test ! -s "${ROOT_DIR}/proc/modules"
make_root
touch "${STATE}/rmmod_stuck"
run --apply >"${WORK}/stuck.out" 2>&1 || { cat "${WORK}/stuck.out"; exit 1; }
check "a module that stays busy asks for a reboot and is not a failure" grep -q "The drbd module stays loaded until the node is rebooted" "${WORK}/stuck.out"
check "files are still removed when the module stays loaded" test ! -e "${ROOT_DIR}/etc/modprobe.d/drbd.conf"
rm -f "${STATE}/rmmod_stuck"
make_root
printf 'devices {\n\tglobal_filter = ["r|^/dev/drbd|"]\n\tfilter = [ "a|.*|" ]\n}\n' >"${ROOT_DIR}/etc/lvm/lvm.conf"
run --apply >"${WORK}/lvmonly.out" 2>&1
check "a global_filter holding only the DRBD element is removed" test "$(grep -c global_filter "${ROOT_DIR}/etc/lvm/lvm.conf")" -eq 0
check "other filters stay" grep -q 'filter = \[ "a|.\*|" \]' "${ROOT_DIR}/etc/lvm/lvm.conf"

make_root
printf 'devices {\n\tglobal_filter = [ "r|^/dev/zd|", "r|^/dev/drbd|" ]\n}\n' >"${ROOT_DIR}/etc/lvm/lvm.conf"
run --apply >"${WORK}/lvmlast.out" 2>&1
check "the DRBD element at the end of the list is removed" grep -qF 'global_filter = [ "r|^/dev/zd|" ]' "${ROOT_DIR}/etc/lvm/lvm.conf"

make_root
set +e
run --delete-volumes >"${WORK}/novg.out" 2>&1
code=$?
set -e
check "--delete-volumes without --vg is refused" test "${code}" -eq 2

make_root
mkdir -p "${ROOT_DIR}/var/lib/deckhouse/sds/bin" "${WORK}/nolvm"
cp "${BIN}/lvm" "${ROOT_DIR}/var/lib/deckhouse/sds/bin/lvm"
for tool in bash awk grep sed diff cmp mktemp cp rm mkdir date tee cat tr dirname hostname timeout cut; do
  ln -sf "$(command -v "${tool}")" "${WORK}/nolvm/${tool}"
done
for tool in drbdsetup modinfo rmmod depmod crictl systemctl; do
  ln -sf "${BIN}/${tool}" "${WORK}/nolvm/${tool}"
done
PATH="${WORK}/nolvm" SRV_NODE_CLEANUP_ROOT="${ROOT_DIR}" "${WORK}/nolvm/bash" "${SCRIPT}" --delete-volumes --vg vg0 --apply >"${WORK}/sdslvm.out" 2>&1 || { cat "${WORK}/sdslvm.out"; exit 1; }
check "the lvm of sds-node-configurator is used when the host has none" test "$(tr '\n' ' ' <"${STATE}/lvs")" == "thinpool root pvc-${UUID1}_00000_snap "

faq_script() {
  awk '/^```bash$/ { getline nxt; if (nxt == "#!/usr/bin/env bash") { print nxt; inblock = 1; next } } inblock && /^```$/ { exit } inblock { print }' "$1"
}
check "the script in docs/FAQ.md is the script in the repository" diff <(faq_script "${REPO}/docs/FAQ.md") "${SCRIPT}"
check "the script in docs/FAQ_RU.md is the script in the repository" diff <(faq_script "${REPO}/docs/FAQ_RU.md") "${SCRIPT}"

if [[ ${FAILED} -ne 0 ]]; then
  exit 1
fi
