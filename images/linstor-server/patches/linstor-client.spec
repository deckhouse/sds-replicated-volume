# this file taked from make rpm and then cat build/bdist.linux-x86_64/rpm/SPECS/linstor-client.spec
# Flant's fixes prefixed (in comments) with FLANT: ...

# FLANT: In ALTLinux was some weird error with _ (underscore) and - (minus) in package filenames. So fix it here with change - to _

%define name linstor_client
# FLANT: change version dymamically
%define version <VERSION>
# FLANT: change version dymamically
%define unmangled_version <VERSION>
%define release 1

Summary: DRBD distributed resource management utility
Name: %{name}
Version: %{version}
Release: %{release}
Source0: %{name}-%{unmangled_version}.tar.gz
License: GPLv3
Group: System Environment/Daemons
BuildRoot: %{_tmppath}/%{name}-%{version}-%{release}-buildroot
Prefix: %{_prefix}
BuildArch: noarch
Vendor: LINBIT HA-Solutions GmbH
Packager: LINSTOR Team <drbd-user@lists.linbit.com>
# FLANT: In ALTLinux was some weird error with _ (underscore) and - (minus) in package filenames. So fix it here with change - to _
Requires:  python_linstor >= 1.19.0
Url: https://www.linbit.com
# FLANT: change to package names for ALTLinux
BuildRequires:  python3-module-setuptools

%description
This client program communicates to controller node which manages the resources

%prep
%setup -n %{name}-%{unmangled_version} -n %{name}-%{unmangled_version}

%build
/usr/bin/python3 setup.py build

%install
/usr/bin/python3 setup.py install --single-version-externally-managed -O1 --root=$RPM_BUILD_ROOT --record=INSTALLED_FILES

%clean
rm -rf $RPM_BUILD_ROOT

%files -f INSTALLED_FILES
# FLANT: Comment out because of error 'Non-white space follows %defattr() ...'
# %defattr(-,root,root)