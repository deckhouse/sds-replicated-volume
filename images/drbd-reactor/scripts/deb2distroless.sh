#!/bin/bash

# based on https://github.com/stickr-eng/heroku-buildpack-deb/blob/master/bin/compile
DEB_FILE=$1

IAM=$(basename $0)
EXCLUDED_DIRS=("etc/init.d" "lib/systemd" "usr/share/doc" "usr/share/man")

function log() {
	local prefix="[$IAM]"
	echo "${prefix} $@"
}

if [ -z "${DEB_FILE}" -o ! -r "${DEB_FILE}" ]; then
	log "ERROR: Package ${DEB_FILE} not found!"
  exit 2
fi

DEB_BASENAME=$(basename $DEB_FILE)
# remove the last hyphen and everything after it
PKG_NAME=${DEB_BASENAME%-*}

EXTRACT_DIR="${2:-"/relocate/${PKG_NAME}"}"
mkdir -p "${EXTRACT_DIR}"

log "Installing $PKG_NAME from $DEB_FILE to ${EXTRACT_DIR}..."

cd "${EXTRACT_DIR}"
ar vx $DEB_FILE

if [ -f "data.tar.xz" ]; then
	log "xz archive detected"
	tar -xJvf data.tar.xz
	rm data.tar.xz
elif [ -f "data.tar.zst" ]; then
	log "zstd archive detected"
	tar --zstd -xvf data.tar.zst
	rm data.tar.zst
elif [ -f "data.tar.gz" ]; then
	log "gz archive detected"
	tar -xzvf data.tar.gz
	rm data.tar.gz
fi

# remove unnecessary files
rm -f debian-binary control.tar.*

# generate a find-command to exclude some directories
# https://stackoverflow.com/a/16595367
FIND_EXCLUDED_DIRS=()

len=${#EXCLUDED_DIRS[@]}
cur=0

if [ $len -gt 0 ]; then
	for dir in ${EXCLUDED_DIRS[@]}; do
	  dir_name="${EXTRACT_DIR}/${dir}"
		if [ -d "${dir_name}" ]; then
	    log "Remove directory '${dir_name}' because its excluded"
			rm -rf "${dir_name}"
		fi
		# cur=$((cur + 1))
		# FIND_EXCLUDED_DIRS+=("-not" "\\(" "-path" "'./$dir'" "-prune" "\\)")
		# if [[ "$cur" -lt "$len" ]]; then
		#   # not a last element, add -or
		# 	FIND_EXCLUDED_DIRS+=("-o")
		# fi
	done
	# FIND_EXCLUDED_DIRS+=("-prune" "\\)")
fi
#find_cmd="find . ${FIND_EXCLUDED_DIRS[@]} -type f -print"
#| xargs -0 -I {} -t cp -vf --parents {} /"
# echo "find cmd: ${find_cmd}"
# find . -not \( -path ./lib/systemd -prune \) -not \( -path ./usr/share/doc -prune \) -type f -print
# without eval I get error like find: paths must precede expression: `\(' :-(
#eval ${find_cmd}
