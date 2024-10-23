#!/bin/bash

# https://github.com/stickr-eng/heroku-buildpack-deb/blob/master/bin/compile
# DEB_FILE=$1

IAM=$(basename $0)
EXCLUDED_DIRS=("etc/init.d" "lib/systemd" "usr/share/doc" "usr/share/man")
BIN_DIRS=("usr/sbin" "usr/bin" "bin" "sbin")

function log() {
	local prefix="[$IAM]"
	echo "${prefix} $@"
}

if ! [ -x "$(command -v apt-get)" ]; then
  log "ERROR: apt-get is not installed." >&2
  exit 1
fi

if ! [ -x "$(command -v dpkg-deb)" ]; then
  log "ERROR: dpkg-deb is not installed." >&2
  exit 1
fi

if [ -z "$( ls -A '/var/lib/apt/lists' )" ]; then
  log "ERROR: You must do 'apt-get update' before use this script."
	exit 3
fi

function download() {
	local download_pkg=$1
	local download_dir=$2

	log "Downloading ${download_pkg} to ${download_dir}..."
	mkdir -p $download_dir

	apt-get --download-only -o Dir::Cache="${download_dir}/" -o Debug::NoLocking=1 install ${download_pkg}
}

function extract() {
	local deb_file=$1
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${2:-"/relocate/${pkg_name}"}"
	local download_dir=$(pwd)

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
	local deb_file=$1
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${2:-"/relocate/${pkg_name}"}"

	for dir in ${BIN_DIRS[@]}; do
	  dir_name="${extract_dir}/${dir}"
		if [ ! -d "${dir_name}" ]; then
			continue
		fi

		# find only non-empty binary files (https://stackoverflow.com/a/47678132)
    while IFS= read -r -d '' binfile; do
			if [ $(grep -IL . "${binfile}") ]; then
        log "[extract_libraries] Process binary file: $binfile"
			fi
    done < <(find ${dir_name} -type f ! -size 0 -print0)
	done

# ldd /bin/bash | awk '/=>/{print $(NF-1)}'  |
#  while read n; do apt-file search $n; done |
#   awk '{print $1}' | sed 's/://' | sort | uniq
}

extract $@
extract_libraries $@

# generate a find-command to exclude some directories
# https://stackoverflow.com/a/16595367

# find . -not \( -path ./lib/systemd -prune \) -not \( -path ./usr/share/doc -prune \) -type f -print
