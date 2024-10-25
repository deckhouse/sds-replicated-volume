#!/bin/bash
# More safety, by turning some bugs into errors.
set -o errexit -o pipefail -o noclobber -o nounset

# https://github.com/stickr-eng/heroku-buildpack-deb/blob/master/bin/compile
# DEB_FILE=$1

IAM=$(basename $0)

# work with getopt as in https://stackoverflow.com/a/29754866
# ignore errexit with `&& true`
getopt --test > /dev/null && true
if [[ $? -ne 4 ]]; then
    echo 'ERROR: `getopt --test` failed in this environment.'
    exit 1
fi

OPTIND=1 # Reset in case getopts has been used previously in the shell.

LONGOPTS=download-dir:,help,extract-dir:,post-process
OPTIONS=d:he:p

# -temporarily store output to be able to check for errors
# -activate quoting/enhanced mode (e.g. by writing out "--options")
# -pass arguments only via   -- "$@"   to separate them correctly
# -if getopt fails, it complains itself to stdout
PARSED=$(getopt --options=$OPTIONS --longoptions=$LONGOPTS --name "$0" -- "$@") || exit 2
# read getopt’s output this way to handle the quoting right:
eval set -- "$PARSED"

REQUIRED_CMDS=("apt-get" "dpkg-deb" "apt-file")
EXCLUDED_DIRS=("etc/init.d" "lib/systemd" "usr/share/doc" "usr/share/man")
BIN_DIRS=("usr/sbin" "usr/bin" "bin" "sbin")

EXTRACT_DIR="/relocate"
DOWNLOAD_DIR="/download-cache"
POST_PROCESS=0

# now enjoy the options in order and nicely split until we see --
while true; do
    case "$1" in
        -d|--download-dir)
            DOWNLOAD_DIR="$2"
            shift 2
            ;;

        -h|--help)
            show_help
            exit 0
            ;;
        -e|--extract-dir)
            EXTRACT_DIR="$2"
            shift 2
            ;;
        -p|--post-process)
            POST_PROCESS=1
            shift
            ;;
        --)
            shift
            break
            ;;
        *)
            echo "Programming error"
            exit 3
            ;;
    esac
done

if [[ $# -ne 1 && $POST_PROCESS -ne 1 ]]; then
    echo "$0: A deb-package file is required."
    exit 4
fi

DEB_FILE="${1:-nonexist.deb}"
DEB_BASENAME=$(basename $DEB_FILE)
# remove the last hyphen and everything after it
PKG_NAME=${DEB_BASENAME%-*}

# [ "${1:-}" = "--" ] && shift

for cmd in ${REQUIRED_CMDS[@]}; do
  if ! [ -x "$(command -v ${cmd})" ]; then
    log "ERROR: ${cmd} is not installed." >&2
    exit 2
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

function log() {
	local prefix="[$IAM]"
	echo "${prefix} $@"
}

function show_help() {
	echo "Usage: ${IAM} [options] [package.deb]"
	echo "Options:"
	echo "--download-dir/-d <directory> - Download directory for depends packages. Default: /download-cache"
	echo "--extract-dir/-e <directory> - Extract (install) directory for package.deb. Default: /relocate"
	echo "--post-process/-p - Install depends from <dowload-dir> to <extract-dir>."
}

function download() {
	local download_pkg=$1
	local download_dir=$2

	log "[download] Downloading ${download_pkg} to ${download_dir}..."
	mkdir -p $download_dir
	chown -R _apt:root ${download_dir}
	cd ${download_dir}

	# apt-get --download-only -o Dir::Cache="${download_dir}/" -o Debug::NoLocking=1 install ${download_pkg}
	apt-get download -o Debug::NoLocking=1 ${download_pkg}
}

function extract() {
	local deb_file="${1:-$DEB_FILE}"
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${EXTRACT_DIR}"
	#local download_dir="${DOWNLOAD_DIR:-"/download-cache"}"

  if [ -z "${deb_file}" -o ! -r "${deb_file}" ]; then
  	log "[extract] ERROR: Package ${deb_file} not found!"
    exit 2
  fi

  mkdir -p "${extract_dir}"

  log "[extract] Installing $pkg_name from $deb_file to ${extract_dir}..."

  cd "${extract_dir}"
  ar x $deb_file

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
	local deb_file="${1:-$DEB_FILE}"
  local deb_basename=$(basename $deb_file)
  # remove the last hyphen and everything after it
  local pkg_name=${deb_basename%-*}

	local extract_dir="${EXTRACT_DIR}"

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

	log "[process_binary] $bin"

	mapfile -t packages < <(ldd "$bin" | awk '/=>/{print $(NF-1)}' |
	while read n; do apt-file search $n; done |
  awk '{print $1}' | sed 's/://' | sort | uniq)

	for pkg in ${packages[@]}; do
		download "${pkg}" "${download_dir}"
	done
}

function cleanup() {
	for dir in ${EXCLUDED_DIRS[@]}; do
	  dir_name="${EXTRACT_DIR}/${dir}"
		if [ -d "${dir_name}" ]; then
	    log "[cleanup] Remove excluded directory '${dir_name}'"
			rm -rf "${dir_name}"
		fi
	done
}

if [ $POST_PROCESS -eq 0 ]; then
  extract
  extract_libraries
else
  log "[Post process] Installing all packages from ${DOWNLOAD_DIR} to ${EXTRACT_DIR}..."

	for f in ${DOWNLOAD_DIR}/*.deb; do
		extract "$f"
	done
	cleanup
fi
