# Linstor Migrator E2E Testing (sds-replicated-volume)

These tests are intended **exclusively for manual execution** and are not integrated into CI.
Due to the specifics of the control plane migration (switching old components to new ones), each scenario requires a clean cluster state.

## Testing Approach

Tests are divided into independent scenarios. **IMPORTANT:** Do not run all tests at once! Top-level `Describe` blocks in Ginkgo are executed sequentially on the same cluster. If the first migration has already passed, the second one will fail because the cluster will already be on the new control plane.

The testing lifecycle looks like this:
1. Run **one** specific scenario via `make test-scenario` (requires a `LABEL`, see below).
2. Manually run the cleanup script (`scripts/cleanup.sh`) **directly on the master node of the test cluster**. (This script does not support local execution from a developer's machine).
3. Run the next scenario on the cleaned cluster.

### Implemented Scenarios (Describe blocks):
* **`All RVs are created with ConfigurationMode: Auto`** (Label: `"Auto"`): The base flow where all Linstor resources have associated RSCs before migration starts.
* **`All RVs are created with ConfigurationMode: Manual`** (Label: `"Manual"`): Emulates a situation where all RSCs were deleted before migration starts.
* **`RVs are created with mixed ConfigurationMode (Auto and Manual)`** (Label: `"Mixed"`): A hybrid scenario where some resources have RSCs and some do not.
* **`Linstor resources without PV`** (Label: `"WithoutPV"`): Emulates a situation where PVCs and PVs are lost before migration to verify correct handling of orphaned ReplicatedVolumes (setting the `no-persistent-volume` label and resolving `replicatedStorageClassName`).

## Environment Setup (Variables)

To run the tests, you need to prepare a file exporting environment variables (e.g., `test_exports.sh`).

### Step 1: Create a new test cluster (First run)
To run the first scenario, the cluster must be created from scratch.

```bash
# --- Persistent variables
export TEST_CLUSTER_NAMESPACE='e2e-linstor-migrator'
export TEST_CLUSTER_STORAGE_CLASS='huawei-storage-stable'
export KUBE_CONFIG_PATH=$HOME/.kube/config-san

# If the test is interrupted by ctrl+c, the next run should see this variable
export TEST_CLUSTER_FORCE_LOCK_RELEASE='true'

# The PR to which the new control plane will switch
export TEST_MIGRATOR_MPO_IMAGE_TAG=pr631

# More logs during test execution
export TEST_DEBUG=1
export TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME=e2e-host

export DKP_LICENSE_KEY=xxxxxxx
export REGISTRY_DOCKER_CFG=<base64>

# --- Cluster creation
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
export TEST_CLUSTER_CLEANUP='false'

# Base cluster
export SSH_HOST=172.17.1.67
export SSH_USER=tfadm
export SSH_PRIVATE_KEY=$HOME/.ssh/flant/id_rsa
export SSH_PUBLIC_KEY=$HOME/.ssh/flant/id_rsa.pub
# Set only if the key has a passphrase, otherwise unset
export SSH_PASSPHRASE=xxxxxxx
```

### Step 2: Use existing cluster (Subsequent runs)
After the first test has completed, **you must clean up the test cluster** (see the "Cleanup" section).
Then change the variables to run the next test on the already existing cluster:

```bash
# ... (persistent variables remain the same) ...

# --- Use previously created cluster
export TEST_CLUSTER_CREATE_MODE='alwaysUseExisting'

# Base cluster (jump host)
export SSH_JUMP_HOST=172.17.1.67
export SSH_JUMP_USER=tfadm

# Test (previously created) cluster
export SSH_HOST=10.211.1.6
export SSH_USER=cloud

# ID of previously saved state (only if you want to run post-checks without re-migration)
# Example: make test-scenario-it LABEL="Auto" FOCUS="RV resource checks"
# Do not set if you don't know why it's needed!
# export TEST_PREVIOUS_RUNID=5a6eaefb
```

## Running Tests

Due to the need to isolate scenarios, the standard `make test` command has been removed.
Use `make test-scenario` and `make test-scenario-it`:

- **`LABEL`** — Ginkgo label for the scenario (`Auto`, `Manual`, `Mixed`, `WithoutPV`).
- **`FOCUS`** — argument for `-ginkgo.focus` (regular expression matching the `Describe` / `It` chain in Ginkgo).

Run entire scenario by label (migration and all nested checks):
```bash
make test-scenario LABEL="Auto"
```

Run specific check inside a scenario:
```bash
make test-scenario-it LABEL="Auto" FOCUS="RV resource checks"
```

## Cleanup

After a scenario completes successfully or fails, the test resources must be completely removed from the cluster before running the next test.

**Warning:** The cleanup script cannot be run from a developer's machine!
1. Copy the script to the master node of the test cluster: `scp scripts/cleanup.sh <USER>@<CLUSTER_IP>:/tmp/`
2. SSH into the test cluster.
3. Run the script: `bash /tmp/cleanup.sh`
