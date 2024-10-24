#!/bin/bash

# https://github.com/stickr-eng/heroku-buildpack-deb/blob/master/bin/compile
# DEB_FILE=$1

IAM=$(basename $0)

DEB_FILE=$1

DEB_BASENAME=$(basename $DEB_FILE)
# remove the last hyphen and everything after it
PKG_NAME=${DEB_BASENAME%-*}

EXTRACT_DIR="${2:-"/relocate/${PKG_NAME}"}"
DOWNLOAD_DIR="${3:-"/download-cache"}"

REQUIRED_CMDS=("apt-get" "dpkg-deb" "apt-file")
EXCLUDED_DIRS=("etc/init.d" "lib/systemd" "usr/share/doc" "usr/share/man")
BIN_DIRS=("usr/sbin" "usr/bin" "bin" "sbin")

function log() {
	local prefix="[$IAM]"
	echo "${prefix} $@"
}

for cmd in ${REQUIRED_CMDS[@]}; do
  if ! [ -x "$(command -v ${cmd})" ]; then
    log "ERROR: ${cmd} is not installed." >&2
    exit 1
  fi
done

if [ -z "$( ls -A '/var/lib/apt/lists' )" ]; then
  log "ERROR: You must do 'apt-get update' before use this script."
	exit 3
fi

# https://salsa.debian.org/apt-team/apt-file/-/blob/master/debian/apt-file.is-cache-empty
if ! apt-get indextargets --format '$(CREATED_BY)' | grep -q ^Contents- ; then
  log "ERROR: You must do 'apt-file update' before use this script."
	exit 3
fi

function download() {
	local download_pkg=$1
	local download_dir=$2

	log "Downloading ${download_pkg} to ${download_dir}..."
	mkdir -p $download_dir
	chown -R _apt:root ${download_dir}
	cd ${download_dir}

	# apt-get --download-only -o Dir::Cache="${download_dir}/" -o Debug::NoLocking=1 install ${download_pkg}
	apt-get download -o Debug::NoLocking=1 ${download_pkg}
}

function extract() {
	local deb_file=${DEB_FILE}
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${EXTRACT_DIR:-"/relocate/${pkg_name}"}"
	#local download_dir="${DOWNLOAD_DIR:-"/download-cache"}"

  if [ -z "${deb_file}" -o ! -r "${deb_file}" ]; then
  	log "ERROR: Package ${deb_file} not found!"
    exit 2
  fi

  mkdir -p "${extract_dir}"

  log "Installing $pkg_name from $deb_file to ${extract_dir}..."

  cd "${extract_dir}"
  ar vx $deb_file

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

	for dir in ${EXCLUDED_DIRS[@]}; do
	  dir_name="${extract_dir}/${dir}"
		if [ -d "${dir_name}" ]; then
	    log "Remove directory '${dir_name}' because its excluded"
			rm -rf "${dir_name}"
		fi
	done

	# apt-cache depends --recurse --no-recommends --no-suggests \
	# --no-conflicts --no-breaks --no-replaces --no-enhances \
	# --no-pre-depends ${f} | grep "^\w"

	# IFS=$'\n' depends=( $(dpkg-deb -I $deb_file | grep -E "Depends|Recommends|Suggests|Pre\-Depends" | tr -d "|," | sed "s/([^)]*)/()/g" | tr -d "()" | tr " " "\n" | grep -Ev "Depends|Recommends|Suggests|Pre\-Depends") )
	# for dep in "${depends[@]}"; do
	# 	echo "dep: ${dep}"
	# 	download "$dep" "${download_dir}"
	# done
}

function extract_libraries() {
	local deb_file=${DEB_FILE}
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${EXTRACT_DIR:-"/relocate/${pkg_name}"}"

	for dir in ${BIN_DIRS[@]}; do
	  dir_name="${extract_dir}/${dir}"
		if [ ! -d "${dir_name}" ]; then
			continue
		fi

		# find only non-empty binary files (https://stackoverflow.com/a/47678132)
    while IFS= read -r -d '' binfile; do
			if [ $(grep -IL . "${binfile}") ]; then
				process_binary "${binfile}"
			fi
    done < <(find ${dir_name} -type f ! -size 0 -print0)
	done
}

function process_binary() {
	local bin="$1"
	local download_dir="${DOWNLOAD_DIR}"

	log "[process_binary] Process binary file: $bin"

	mapfile -t packages < <(ldd "$bin" | awk '/=>/{print $(NF-1)}' |
	while read n; do apt-file search $n; done |
  awk '{print $1}' | sed 's/://' | sort | uniq)

	for pkg in ${packages[@]}; do
		download "${pkg}" "${download_dir}"
	done
}

extract $@
extract_libraries $@

ls -laR ${DOWNLOAD_DIR}

# generate a find-command to exclude some directories
# https://stackoverflow.com/a/16595367

# find . -not \( -path ./lib/systemd -prune \) -not \( -path ./usr/share/doc -prune \) -type f -print
