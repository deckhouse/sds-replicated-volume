- [Основные положения](#основные-положения)
  - [Схема именования акторов](#схема-именования-акторов)
- [Контракт данных: `ReplicatedVolume`](#контракт-данных-replicatedvolume)
  - [`spec`](#spec)
  - [`status`](#status)
    - [`status.conditions`](#statusconditions)
    - [`status.config`](#statusconfig)
    - [`status.publishedOn`](#statuspublishedon)
    - [`status.actualSize`](#statusactualsize)
    - [`status.phase`](#statusphase)
- [Контракт данных: `ReplicatedVolumeReplica`](#контракт-данных-replicatedvolumereplica)
  - [`spec`](#spec-1)
  - [`status`](#status-1)
    - [`status.conditions`](#statusconditions-1)
    - [`status.config`](#statusconfig-1)
    - [`status.drbd`](#statusdrbd)
- [Акторы приложения: `agent`](#акторы-приложения-agent)
  - [`drbd-config-controller`](#drbd-config-controller)
  - [`rvr-delete-controller`](#rvr-delete-controller)
  - [`drbd-resize-controller`](#drbd-resize-controller)
  - [`drbd-primary-controller`](#drbd-primary-controller)
  - [`rvr-drbd-status-controller`](#rvr-drbd-status-controller)
  - [`rvr-status-config-address-controller` \[OK | priority: 5 | complexity: 3\]](#rvr-status-config-address-controller-ok--priority-5--complexity-3)
- [Акторы приложения: `controller`](#акторы-приложения-controller)
  - [`rvr-add-controller` \[OK | priority: 5 | complexity: 3\]](#rvr-add-controller-ok--priority-5--complexity-3)
  - [`rvr-node-selector-controller`](#rvr-node-selector-controller)
  - [`rvr-status-config-node-id-controller` \[OK | priority: 5 | complexity: 1\]](#rvr-status-config-node-id-controller-ok--priority-5--complexity-1)
  - [`rvr-status-config-peers-controller` \[OK | priority: 5 | complexity: 3\]](#rvr-status-config-peers-controller-ok--priority-5--complexity-3)
  - [`rv-primary-rvr-controller`](#rv-primary-rvr-controller)
  - [`rvr-volume-controller`](#rvr-volume-controller)
  - [`rvr-gc-controller`](#rvr-gc-controller)
  - [`rv-status-config-controller`](#rv-status-config-controller)
  - [`rv-status-config-quorum-controller`](#rv-status-config-quorum-controller)
  - [`rv-status-config-shared-secret-controller` \[OK | priority: 1 | complexity: 2\]](#rv-status-config-shared-secret-controller-ok--priority-1--complexity-2)
  - [`rv-status-controller` \[OK\]](#rv-status-controller-ok)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller)
  - [`rvr-node-cordon-controller`](#rvr-node-cordon-controller)
- [Сценарии](#сценарии)
  - [Ручное создание реплицируемого тома](#ручное-создание-реплицируемого-тома)

# Основные положения


## Схема именования акторов
`{controlledEntity}-{name}-{actorType}`
где
 - `controlledEntity` - название сущности под контролем актора
 - `name` - имя актора, указывающее на его основную цель
 - `actorType` - тип актора (`controller`, `scanner`, `worker`)

# Контракт данных: `ReplicatedVolume`
## `spec`
 - `spec.size`
 - `spec.replicatedStorageClassName`
 - `spec.publishOn[]`

## `status`
### `status.conditions`
 - `status.conditions[].type=Ready`
   - `status.conditions[].status`:
     - `True` — все подстатусы/предикаты успешны
     - `False` — есть несоответствия, подробности в `reason`/`message`
   - `status.conditions[].reason`/`message` — вычисляются контроллером
### `status.config`
 - `status.config.sharedSecret`
 - `status.config.sharedSecretAlg`
 - `status.config.quorum`
 - `status.config.quorumMinimumRedundancy`
 - `status.config.allowTwoPrimaries`
 - `status.config.deviceMinor`
### `status.publishedOn`
 - `status.publishedOn[]`
### `status.actualSize`
 - `status.actualSize`

### `status.phase`
 - `Terminating`
 - `Synchronizing`
 - `Ready`

# Контракт данных: `ReplicatedVolumeReplica`
## `spec`
 - `spec.replicatedVolumeName`
 - `spec.nodeName`
 - `spec.diskless`

## `status`
### `status.conditions`
 - `status.conditions[].type=Ready`
   - `status.conditions[].reason`:
     - `WaitingForInitialSync`
     - `DevicesAreNotReady`
     - `AdjustmentFailed`
     - `NoQuorum`
     - `DiskIOSuspended`
     - `Ready`
 - `status.conditions[].type=InitialSync`
   - `status.conditions[].reason`:
     - `InitialSyncRequiredButNotReady`
     - `SafeForInitialSync`
     - `InitialDeviceReadinessReached`
 - `status.conditions[].type=Primary`
   - `status.conditions[].reason`:
     - `ResourceRoleIsPrimary`
     - `ResourceRoleIsNotPrimary`
 - `status.conditions[].type=DevicesReady`
   - `status.conditions[].reason`:
     - `DeviceIsNotReady`
     - `DeviceIsReady`
 - `status.conditions[].type=ConfigurationAdjusted`
   - `status.conditions[].reason`:
     - `ConfigurationFailed`
     - `MetadataCheckFailed`
     - `MetadataCreationFailed`
     - `StatusCheckFailed`
     - `ResourceUpFailed`
     - `ConfigurationAdjustFailed`
     - `ConfigurationAdjustmentPausedUntilInitialSync`
     - `PromotionDemotionFailed`
     - `ConfigurationAdjustmentSucceeded`
 - `status.conditions[].type=Quorum`
   - `status.conditions[].reason`:
     - `NoQuorumStatus`
     - `QuorumStatus`
 - `status.conditions[].type=DiskIOSuspended`
   - `status.conditions[].reason`:
     - `DiskIONotSuspendedStatus`
     - `DiskIOSuspendedUnknownReason`
     - `DiskIOSuspendedByUser`
     - `DiskIOSuspendedNoData`
     - `DiskIOSuspendedFencing`
     - `DiskIOSuspendedQuorum`
### `status.config`
 - `status.config.nodeId`
 - `status.config.address.ipv4`
 - `status.config.address.port`
 - `status.config.peers`:
   - `peer.nodeId`
   - `peer.address.ipv4`
   - `peer.address.port`
   - `peer.diskless`
 - `status.config.disk`
 - `status.config.primary`
### `status.drbd`
 - `status.drbd.name`
 - `status.drbd.nodeId`
 - `status.drbd.role`
 - `status.drbd.suspended`
 - `status.drbd.suspendedUser`
 - `status.drbd.suspendedNoData`
 - `status.drbd.suspendedFencing`
 - `status.drbd.suspendedQuorum`
 - `status.drbd.forceIOFailures`
 - `status.drbd.writeOrdering`
 - `status.drbd.devices[]`:
   - `volume`
   - `minor`
   - `diskState`
   - `client`
   - `open`
   - `quorum`
   - `size`
   - `read`
   - `written`
   - `alWrites`
   - `bmWrites`
   - `upperPending`
   - `lowerPending`
 - `status.drbd.connections[]`:
   - `peerNodeId`
   - `name`
   - `connectionState`
   - `congested`
   - `peerRole`
   - `tls`
   - `apInFlight`
   - `rsInFlight`
   - `paths[]`:
     - `thisHost.address`
     - `thisHost.port`
     - `thisHost.family`
     - `remoteHost.address`
     - `remoteHost.port`
     - `remoteHost.family`
     - `established`
   - `peerDevices[]`:
     - `volume`
     - `replicationState`
     - `peerDiskState`
     - `peerClient`
     - `resyncSuspended`
     - `outOfSync`
     - `pending`
     - `unacked`
     - `hasSyncDetails`
     - `hasOnlineVerifyDetails`
     - `percentInSync`

# Акторы приложения: `agent`

## `drbd-config-controller`

### Цель 
Контроллирует DRBD конфиг на ноде для всех rvr (в том числе удалённых, с
неснятым финализатором контроллера).


### Триггер 
  - 
  
### Вывод 
  - 

## `rvr-delete-controller`

### Цель 

### Триггер 
  - 
### Вывод 
  - 

## `drbd-resize-controller`

### Цель 


### Триггер 
  - 
### Вывод 
  - 

## `drbd-primary-controller`

### Цель 

### Триггер 
  - 
### Вывод 
  - 

## `rvr-drbd-status-controller`

### Цель 

### Триггер 
  - 
### Вывод 
  - 

## `rvr-status-config-address-controller` [OK | priority: 5 | complexity: 3]
### Цель 
### Триггер 
  - 
### Вывод 
  - 


# Акторы приложения: `controller`

## `rvr-add-controller` [OK | priority: 5 | complexity: 3]

### Цель 
Добавлять привязанные реплики (RVR) для RV.

Целевое количество реплик определяется в `ReplicatedStorageClass`.

Первая реплика должна перейти в полностью работоспособное состояние, прежде чем
будет создана вторая реплика. Вторая и последующие реплики могут быть созданы
параллельно.

### Триггер 
  - `CREATE(RV)`, `UPDATE(RVR[metadata.deletionTimestamp -> !null])`
    - когда фактическое количество реплик (в том числе неработоспособных, но исключая удаляемые) меньше требуемого
  - `UPDATE(RVR[status.conditions[type=Ready].Status == True])`
    - когда фактическое количество реплик равно 1

### Вывод 
  - создаёт RVR вплоть до RV->
[RSC->`spec.replication`](https://deckhouse.io/modules/sds-replicated-volume/stable/cr.html#replicatedstorageclass-v1alpha1-spec-replication)
    - `spec.replicatedVolumeName` имеет значение RV `metadata.name`
    - `metadata.ownerReferences` указывает на RV по имени `metadata.name`

## `rvr-node-selector-controller`

### Цель 

Исключать закордоненные ноды (см. `rvr-node-cordon-controller`)

### Триггер 
  - 
### Вывод 
  - 


## `rvr-status-config-node-id-controller` [OK | priority: 5 | complexity: 1]
### Цель
Проставить свойству `rvr.status.config.nodeId` уникальное значение среди всех реплик одной RV, в диапазоне [0; 7].

В случае превышения количества реплик, повторять реконсайл с ошибкой.

### Триггер 
  - `CREATE(RVR, status.config.nodeId==nil)`

### Вывод 
  - `rvr.status.config.nodeId`

## `rvr-status-config-peers-controller` [OK | priority: 5 | complexity: 3]

### Цель 
Поддерживать актуальное состояние пиров на каждой реплике.

### Триггер 
  - `INIT(RV)`
  - `CREATE/UPDATE(RVR, spec.nodeName!=nil, spec.diskless!=nil, status.nodeId !=nil, status.address != nil)`
  - `DELETE(RVR)`
### Вывод 
  - `rvr.status.peers`

## `rv-primary-rvr-controller`

### Цель 

Следить за `rv.spec.publishOn`, менять `rv.status.allowTwoPrimaries`, дожидаться фактического применения настройки, и обновлять `rvr.status.config.primary` 

Должен учитывать фактическое состояние `rvr.status.drbd.connections[].peerRole` и не допускать двух.


### Триггер 
  - 
### Вывод 
  - `rvr.status.config.primary`

## `rvr-volume-controller`

### Цель 

### Триггер 
  - 
### Вывод 
  - 

## `rvr-gc-controller`

### Цель 

Нельзя снимать финализатор, пока rvr Primary (де-факто).

Снять финализатор, когда есть необходимое количество рабочих реплик в кластере,
завершим тем самым удаление, вызванное по любой другой причине.

### Триггер 
  - 

### Вывод 

## `rv-status-config-controller`

### Цель 
Сконфигурировать первоначальные общие настройки для всех реплик, указываемые в `rv.status.config`.

### Триггер
 - `CREATE(RV, rv.status.config == nil)`

### Вывод 
  - `rv.status.config.sharedSecret`
  - `rv.status.config.sharedSecretAlg`
  - `rv.status.config.quorum`
  - `rv.status.config.quorumMinimumRedundancy`
  - `rv.status.config.allowTwoPrimaries`
  - `rv.status.config.deviceMinor`


 <!-- - `status.config.`
 - `status.config.nodeAddress.ipv4`
 - `status.config.nodeAddress.port`
 - `status.config.peers`:
   - `peer.nodeId`
   - `peer.nodeAddress.ipv4`
   - `peer.nodeAddress.port`
   - `peer.diskless`
 - `status.config.disk`
 - `status.config.primary` -->

## `rv-status-config-quorum-controller`
### Цель 

Для проставления кворума - дождаться наличие рабочих реплик.

### Триггер

### Вывод 

## `rv-status-config-shared-secret-controller` [OK | priority: 1 | complexity: 2]

### Цель
Проставить первоначальное значения для `rv.status.config.sharedSecret` и `rv.status.config.sharedSecretAlg`,
а также обработать ошибку приминения алгоритма на любой из реплик из `rvr.status.conditions[Type=ConfigurationAdjusted,Status=False,Reason=UnsupportedAlgorithm]`, и поменять его на следующий по списку. Последний проверенный алгоритм должен быть указан в `Message`.
В случае, если список закончился, выставить для `rv.status.conditions[Type=SharedSecretAlgorithmSelected].Status=False` `Reason=UnableToSelectSharedSecretAlgorithm`

### Триггер
 - `CREATE(RV, rv.status.config.sharedSecret == "")`
 - `CREATE/UPDATE(RVR, status.conditions[Type=ConfigurationAdjusted,Status=False,Reason=UnsupportedAlgorithm])`

### Вывод 
 - `rv.status.config.sharedSecret`
   - генерируется новый
 - `rv.status.config.sharedSecretAlg`
   - выбирается из захардкоженного списка по порядку
 - `rv.status.conditions[Type=SharedSecretAlgorithmSelected].Status=False`
 - `rv.status.conditions[Type=SharedSecretAlgorithmSelected].Reason=UnableToSelectSharedSecretAlgorithm`
 - `rv.status.conditions[Type=SharedSecretAlgorithmSelected].Message=[Which node? Which alg failed?]`

## `rv-status-controller` [OK]

### Цель 
Обновить вычисляемые поля статутса RV. 

### Вывод 
 - `rv.status.conditions[Type=Ready]`
   - `Status=True` в случае если все подстатусы успешны, иначе `False`
 - `phase`

### Триггер 
Изменение `rv.status.conditions`

## `rvr-missing-node-controller`

### Цель 
Удаляет (без снятия финализатора) RVR с тех нод, которых больше нет в кластере.

### Триггер 
  - во время INIT/DELETE `corev1.Node`
    - когда Node больше нет в кластере

### Вывод 
  - delete rvr

## `rvr-node-cordon-controller`

### Цель 
Удаляет (без снятия финализатора) RVR с тех нод, которые помечены специальным
образом как закордоненные (аннотация, а не `spec.cordon`).

### Триггер 
  - во время INIT/DELETE `corev1.Node`
    - когда Node помечена специальным
образом как закордоненные (аннотация, а не `spec.cordon`).

### Вывод 
  - delete rvr








# Сценарии

## Ручное создание реплицируемого тома
1. Создаётся RV
   1. `spec.size`
   1. `spec.replicatedStorageClassName`
2. Срабатывает `rv-config-controller`
   1. `rv.status.config.sharedSecret`
   2. `rv.status.config.replicaCount`
   3. и т.д.
3. Срабатывает `rv-replica-count-controller`
   1. Создаётся первая RVR, ожидается её переход в Ready
   2. Создаются остальные RVR вплоть до `rv.status.config.replicaCount`
4. Срабатывает `rvr-node-selector-controller`
   1. Выбирается нода
5. Срабатывает `rvr-volume-controller`
   1. Создается том
   2. Обновляется том в `rvr.status.config.volumes`
6. Срабатывает `rvr-config-controller`
   1. Заполняется `rvr.status.config`
7. На узле срабатывает `rvr-create-controller`
   1. Выполняются необходимые операции в drbd (drbdadm create-md, up, adjust, primary --force)

