#!/bin/bash

crane="/usr/local/bin/crane"
repositoryName=$1
edition=$2
channel=$3
version=$4
user=$5
password=$6

echo "Module $repositoryName, edition $edition, channel $channel, version $version"

if [[ "$channel" == "alpha" ]]; then
 echo "Deploying $version to alpha channel"
 exit 0
elif [[ "$channel" == "beta" ]]; then
 previousChannel="alpha"
elif [[ "$channel" == "early-access" ]]; then
 previousChannel="beta"
elif [[ "$channel" == "stable" ]]; then
 previousChannel="early-access"
elif [[ "$channel" == "rock-solid" ]]; then
 previousChannel="stable"
else
 echo "Unknown channel"
 exit 1
fi

echo "Checking previous channel $previousChannel"
$crane auth login -u $user -p $password registry.deckhouse.io
previousChannelVersion=$($crane export registry.deckhouse.io/deckhouse/$edition/modules/$repositoryName/release:$previousChannel | grep -aoE '\{"version":".*"\}' | jq -r .version)
if [[ "$version" == "$previousChannelVersion" ]]; then
 echo "Previous channel $previousChannel version $previousChannelVersion is equal desired version $version, processing"
 exit 0
else
 echo "Previous channel $previousChannel version $previousChannelVersion is not equal desired version $version, rejecting"
 exit 1
fi
