# this file taked from make rpm and then cat build/bdist.linux-x86_64/rpm/SPECS/python-linstor.spec
# Flant's fixes prefixed (in comments) with FLANT: ...

# FLANT: In ALTLinux was some weird error with _ (underscore) and - (minus) in package filenames. So fix it here with change - to _
%define name python_linstor
# FLANT: change version dymamically
%define version <VERSION>
# FLANT: change version dymamically
%define unmangled_version <VERSION>
%define release 1

Summary: Linstor python api
Name: %{name}
Version: %{version}
Release: %{release}
Source0: %{name}-%{unmangled_version}.tar.gz
License: LGPLv3
Group: System Environment/Daemons
BuildRoot: %{_tmppath}/%{name}-%{version}-%{release}-buildroot
Prefix: %{_prefix}
BuildArch: noarch
Vendor: LINBIT HA-Solutions GmbH
Packager: LINSTOR Team <drbd-user@lists.linbit.com>
Url: https://www.linbit.com
# FLANT: change to package names for ALTLinux
BuildRequires:  python3-module-setuptools

%description
# LINSTOR Python API

This repository contains a Python library to communicate with a linstor controller.

LINSTOR, developed by [LINBIT](https://www.linbit.com), is a software that manages DRBD replicated
LVM/ZFS volumes across a group of machines. It maintains DRBD configuration on the participating machines.  It
creates/deletes the backing LVM/ZFS volumes. It automatically places the backing LVM/ZFS volumes among the
participating machines.

# Online API documentation
A rendered html documentation for the LINSTOR Python API can be found [here](https://linbit.github.io/linstor-api-py/).

# Using Linstor
Please read the user-guide provided at [docs.linbit.com](https://docs.linbit.com).

# Support
For further products and professional support, please
[contact](http://links.linbit.com/support) us.

# Releases
Releases generated by git tags on github are snapshots of the git repository at the given time. You most
likely do not want to use these. They might lack things such as generated man pages, the `configure` script,
and other generated files. If you want to build from a tarball, use the ones [provided by us](https://www.linbit.com/en/drbd-community/drbd-download/).


%prep
%setup -n %{name}-%{unmangled_version} -n %{name}-%{unmangled_version}

%build
python3 setup.py build

%install
python3 setup.py install --single-version-externally-managed -O1 --root=$RPM_BUILD_ROOT --record=INSTALLED_FILES

%clean
rm -rf $RPM_BUILD_ROOT

%files -f INSTALLED_FILES
# FLANT: Comment out because of error 'Non-white space follows %defattr() ...'
# %defattr(-,root,root)