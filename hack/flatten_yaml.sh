#!/usr/bin/env bash

set -euo pipefail

mkdir -p crds/flat

python3 hack/flatten_yaml.py crds/replicatedstorageclass.yaml crds/flat/replicatedstorageclass.txt
python3 hack/flatten_yaml.py crds/storage.deckhouse.io_replicatedstorageclasses.yaml crds/flat/storage.deckhouse.io_replicatedstorageclasses.txt
python3 hack/flatten_yaml.py crds/replicatedstoragepool.yaml crds/flat/replicatedstoragepool.txt
python3 hack/flatten_yaml.py crds/storage.deckhouse.io_replicatedstoragepools.yaml crds/flat/storage.deckhouse.io_replicatedstoragepools.txt

echo "Flattened CRDs written to crds/flat/"

