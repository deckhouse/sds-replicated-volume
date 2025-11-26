- [Основные положения](#основные-положения)
  - [Схема именования акторов](#схема-именования-акторов)
  - [Условное обозначение триггеров](#условное-обозначение-триггеров)
  - [Константы](#константы)
  - [Настройки](#настройки)
- [Контракт данных: `ReplicatedVolume`](#контракт-данных-replicatedvolume)
  - [`spec`](#spec)
    - [`size`](#size)
    - [`replicatedStorageClassName`](#replicatedstorageclassname)
    - [`publishOn[]`](#publishon)
  - [`status`](#status)
    - [`conditions[]`](#conditions)
    - [`config`](#config)
    - [`publishedOn`](#publishedon)
    - [`actualSize`](#actualsize)
    - [`phase`](#phase)
- [Контракт данных: `ReplicatedVolumeReplica`](#контракт-данных-replicatedvolumereplica)
  - [`spec`](#spec-1)
    - [`replicatedVolumeName`](#replicatedvolumename)
    - [`nodeName`](#nodename)
    - [`diskless`](#diskless)
  - [`status`](#status-1)
    - [`conditions[]`](#conditions-1)
    - [`config`](#config-1)
    - [`drbd`](#drbd)
- [Акторы приложения: `agent`](#акторы-приложения-agent)
  - [`drbd-config-controller`](#drbd-config-controller)
    - [Цель](#цель)
    - [Триггер](#триггер)
    - [Вывод](#вывод)
  - [`rvr-delete-controller`](#rvr-delete-controller)
    - [Цель](#цель-1)
    - [Триггер](#триггер-1)
    - [Вывод](#вывод-1)
  - [`drbd-resize-controller`](#drbd-resize-controller)
    - [Цель](#цель-2)
    - [Триггер](#триггер-2)
    - [Вывод](#вывод-2)
  - [`drbd-primary-controller`](#drbd-primary-controller)
    - [Цель](#цель-3)
    - [Триггер](#триггер-3)
    - [Вывод](#вывод-3)
  - [`rvr-drbd-status-controller`](#rvr-drbd-status-controller)
    - [Цель](#цель-4)
    - [Триггер](#триггер-4)
    - [Вывод](#вывод-4)
  - [`rvr-status-config-address-controller`](#rvr-status-config-address-controller)
    - [Статус: \[TBD | priority: 5 | complexity: 3\]](#статус-tbd--priority-5--complexity-3)
    - [Цель](#цель-5)
    - [Триггер](#триггер-5)
    - [Вывод](#вывод-5)
- [Акторы приложения: `controller`](#акторы-приложения-controller)
  - [`rvr-diskful-count-controller`](#rvr-diskful-count-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3)
    - [Цель](#цель-6)
    - [Триггер](#триггер-6)
    - [Вывод](#вывод-6)
  - [`rvr-node-selector-controller`](#rvr-node-selector-controller)
    - [Цель](#цель-7)
    - [Триггер](#триггер-7)
    - [Вывод](#вывод-7)
  - [`rvr-status-config-node-id-controller`](#rvr-status-config-node-id-controller)
    - [Статус: \[OK | priority: 5 | complexity: 1\]](#статус-ok--priority-5--complexity-1)
    - [Цель](#цель-8)
    - [Триггер](#триггер-8)
    - [Вывод](#вывод-8)
  - [`rvr-status-config-peers-controller`](#rvr-status-config-peers-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3-1)
    - [Цель](#цель-9)
    - [Триггер](#триггер-9)
    - [Вывод](#вывод-9)
  - [`rv-publish-controller`](#rv-publish-controller)
    - [Статус: \[TBD | priority: 5 | complexity: 5\]](#статус-tbd--priority-5--complexity-5)
    - [Цель](#цель-10)
    - [Триггер](#триггер-10)
    - [Вывод](#вывод-10)
  - [`rvr-volume-controller`](#rvr-volume-controller)
    - [Цель](#цель-11)
    - [Триггер](#триггер-11)
    - [Вывод](#вывод-11)
  - [`rvr-gc-controller`](#rvr-gc-controller)
    - [Цель](#цель-12)
    - [Триггер](#триггер-12)
    - [Вывод](#вывод-12)
  - [`rv-status-config-controller`](#rv-status-config-controller)
    - [Цель](#цель-13)
    - [Триггер](#триггер-13)
    - [Вывод](#вывод-13)
  - [`rv-status-config-quorum-controller`](#rv-status-config-quorum-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3-2)
    - [Цель](#цель-14)
    - [Триггер](#триггер-14)
    - [Вывод](#вывод-14)
  - [`rv-status-config-shared-secret-controller`](#rv-status-config-shared-secret-controller)
    - [Статус: \[OK | priority: 3 | complexity: 3\]](#статус-ok--priority-3--complexity-3)
    - [Цель](#цель-15)
    - [Триггер](#триггер-15)
    - [Вывод](#вывод-15)
  - [`rv-status-controller` \[TBD\]](#rv-status-controller-tbd)
    - [Цель](#цель-16)
    - [Вывод](#вывод-16)
    - [Триггер](#триггер-16)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller)
    - [Цель](#цель-17)
    - [Триггер](#триггер-17)
    - [Вывод](#вывод-17)
  - [`rvr-node-cordon-controller`](#rvr-node-cordon-controller)
    - [Цель](#цель-18)
    - [Триггер](#триггер-18)
    - [Вывод](#вывод-18)
- [Сценарии](#сценарии)
  - [Ручное создание реплицируемого тома](#ручное-создание-реплицируемого-тома)

# Основные положения


## Схема именования акторов
`{controlledEntity}-{name}-{actorType}`
где
 - `controlledEntity` - название сущности под контролем актора
 - `name` - имя актора, указывающее на его основную цель
 - `actorType` - тип актора (`controller`, `scanner`, `worker`)

## Условное обозначение триггеров
 - `CREATE` - событие создания и синхронизации ресурса; синхронизация происходит для каждого ресурса, при старте контроллера, а также на регулярной основе (раз в 20 часов)
 - `UPDATE` - событие обновления ресурса (в т.ч. проставление `metadata.deletionTimestamp`)
 - `DELETE` - событие окончательного удаления ресурса, происходит после снятия последнего финализатора (может быть потеряно в случае недоступности контроллера)

## Константы
TBD

## Настройки
 - `drbdMinPort` - минимальный порт для использования ресурсами
 - `drbdMaxPort` - максимальный порт для использования ресурсами

# Контракт данных: `ReplicatedVolume`
## `spec`
### `size`
### `replicatedStorageClassName`
### `publishOn[]`

## `status`
### `conditions[]`
 - `type=Ready`
### `config`
 - `sharedSecret`
 - `sharedSecretAlg`
 - `quorum`
 - `quorumMinimumRedundancy`
 - `allowTwoPrimaries`
 - `deviceMinor`
### `publishedOn`
### `actualSize`

### `phase`
 - `Terminating`
 - `Synchronizing`
 - `Ready`

# Контракт данных: `ReplicatedVolumeReplica`
## `spec`
### `replicatedVolumeName`
### `nodeName`
### `diskless`

## `status`
### `conditions[]`
 - `type=Ready`
   - `reason`:
     - `WaitingForInitialSync`
     - `DevicesAreNotReady`
     - `AdjustmentFailed`
     - `NoQuorum`
     - `DiskIOSuspended`
     - `Ready`
 - `type=InitialSync`
   - `reason`:
     - `InitialSyncRequiredButNotReady`
     - `SafeForInitialSync`
     - `InitialDeviceReadinessReached`
 - `type=Primary`
   - `reason`:
     - `ResourceRoleIsPrimary`
     - `ResourceRoleIsNotPrimary`
 - `type=DevicesReady`
   - `reason`:
     - `DeviceIsNotReady`
     - `DeviceIsReady`
 - `type=ConfigurationAdjusted`
   - `reason`:
     - `ConfigurationFailed`
     - `MetadataCheckFailed`
     - `MetadataCreationFailed`
     - `StatusCheckFailed`
     - `ResourceUpFailed`
     - `ConfigurationAdjustFailed`
     - `ConfigurationAdjustmentPausedUntilInitialSync`
     - `PromotionDemotionFailed`
     - `ConfigurationAdjustmentSucceeded`
 - `type=Quorum`
   - `reason`:
     - `NoQuorumStatus`
     - `QuorumStatus`
 - `type=DiskIOSuspended`
   - `reason`:
     - `DiskIONotSuspendedStatus`
     - `DiskIOSuspendedUnknownReason`
     - `DiskIOSuspendedByUser`
     - `DiskIOSuspendedNoData`
     - `DiskIOSuspendedFencing`
     - `DiskIOSuspendedQuorum`
### `config`
 - `nodeId`
 - `address.ipv4`
 - `address.port`
 - `peers`:
   - `peer.nodeId`
   - `peer.address.ipv4`
   - `peer.address.port`
   - `peer.diskless`
 - `disk`
 - `primary`
### `drbd`
 - `name`
 - `nodeId`
 - `role`
 - `suspended`
 - `suspendedUser`
 - `suspendedNoData`
 - `suspendedFencing`
 - `suspendedQuorum`
 - `forceIOFailures`
 - `writeOrdering`
 - `devices[]`:
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
 - `connections[]`:
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

## `rvr-status-config-address-controller`

### Статус: [TBD | priority: 5 | complexity: 3]

### Цель 
Проставить значение свойству `rvr.status.config.address`.
 - `ipv4` - взять из `node.status.addresses[type=InternalIP]`
 - `port` - найти наименьший свободный порт в диапазоне, задаваемом в настройках `drbdMinPort`/`drbdMaxPort`

В случае, если нет свободного порта, настроек порта, либо IP: повторять реконсайл с ошибкой.

### Триггер 
  - `CREATE/UPDATE(RVR, rvr.spec.nodeName, !rvr.status.config.address)`

### Вывод 
  - `rvr.status.config.address`

# Акторы приложения: `controller`

## `rvr-diskful-count-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель 
Добавлять привязанные diskful-реплики (RVR) для RV.

Целевое количество реплик определяется в `ReplicatedStorageClass` (получать через `rv.spec.replicatedStorageClassName`).

Первая реплика должна перейти в полностью работоспособное состояние, прежде чем
будет создана вторая реплика. Вторая и последующие реплики могут быть созданы
параллельно.

### Триггер
  - `CREATE(RV)`, `UPDATE(RVR[metadata.deletionTimestamp -> !null])`
    - когда фактическое количество реплик (в том числе неработоспособных, но исключая удаляемые) меньше требуемого
  - `UPDATE(RVR[status.conditions[type=Ready].status == True])`
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
  - `rvr.spec.nodeName`
  - `rvr.spec.diskless`


## `rvr-status-config-node-id-controller`

### Статус: [OK | priority: 5 | complexity: 1]

### Цель
Проставить свойству `rvr.status.config.nodeId` уникальное значение среди всех реплик одной RV, в диапазоне [0; 7].

В случае превышения количества реплик, повторять реконсайл с ошибкой.

### Триггер
  - `CREATE(RVR, status.config.nodeId==nil)`

### Вывод
  - `rvr.status.config.nodeId`

## `rvr-status-config-peers-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель
Поддерживать актуальное состояние пиров на каждой реплике.

Для любого RV у всех готовых RVR в пирах прописаны все остальные готовые, кроме неё.

Готовая RVR - та, у которой `spec.nodeName!="", status.nodeId !=nil, status.address != nil`

### Триггер
  - `CREATE(RV)`
  - `CREATE/UPDATE(RVR, spec.nodeName!="", status.nodeId !=nil, status.address != nil)`
  - `DELETE(RVR)`
### Вывод
  - `rvr.status.peers`

## `rv-publish-controller`

### Статус: [TBD | priority: 5 | complexity: 5]

### Цель 

Следить за `rv.spec.publishOn`, менять `rv.status.allowTwoPrimaries`, дожидаться фактического применения настройки, и обновлять `rvr.status.config.primary` 

Должен учитывать фактическое состояние `rvr.status.drbd.connections[].peerRole` и не допускать более двух Primary. Два допустимы только во время включенной настройки `allowTwoPrimaries`.

<!-- Работает только когда RV имеет `status.condition[Type=Ready].status=True`.

- Если `volumeAccess=Local`, то он может только менять primary на существующей реплике
- Если `volumeAccess!=Local` - то он может создавать новые реплики сразу с diskless: true -->

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

## `rv-status-config-quorum-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель 

Поднять значение кворума до необходимого, после того как кластер станет работоспособным.

Работоспособный кластер - это полностью готовый и доступный, без учёта отсутствия настройки кворума.

### Триггер
 - `CREATE/UPDATE(RV, rv.status.conditions[type=Ready].status==True)`

### Вывод
  - `rv.status.config.quorum`
  - `rv.status.config.quorumMinimumRedundancy`

Правильные значения, в зависимости от количества diskful реплик N:

```
var quorum byte = N/2 + 1
var qmr byte = 0
if N > 2 {
	qmr = quorum
}
```

## `rv-status-config-shared-secret-controller`

### Статус: [OK | priority: 3 | complexity: 3]

### Цель
Проставить первоначальное значения для `rv.status.config.sharedSecret` и `rv.status.config.sharedSecretAlg`,
а также обработать ошибку приминения алгоритма на любой из реплик из `rvr.status.conditions[type=ConfigurationAdjusted,status=False,reason=UnsupportedAlgorithm]`, и поменять его на следующий по списку. Последний проверенный алгоритм должен быть указан в `Message`.
В случае, если список закончился, выставить для `rv.status.conditions[type=SharedSecretAlgorithmSelected].status=False` `reason=UnableToSelectSharedSecretAlgorithm`

### Триггер
 - `CREATE(RV, rv.status.config.sharedSecret == "")`
 - `CREATE/UPDATE(RVR, status.conditions[type=ConfigurationAdjusted,status=False,reason=UnsupportedAlgorithm])`

### Вывод 
 - `rv.status.config.sharedSecret`
   - генерируется новый
 - `rv.status.config.sharedSecretAlg`
   - выбирается из захардкоженного списка по порядку
 - `rv.status.conditions[type=SharedSecretAlgorithmSelected].status=False`
 - `rv.status.conditions[type=SharedSecretAlgorithmSelected].reason=UnableToSelectSharedSecretAlgorithm`
 - `rv.status.conditions[type=SharedSecretAlgorithmSelected].message=[Which node? Which alg failed?]`

## `rv-status-controller` [TBD]

### Цель 
Обновить вычисляемые поля статутса RV. 

### Вывод 
 - `rv.status.conditions[type=Ready]`
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

