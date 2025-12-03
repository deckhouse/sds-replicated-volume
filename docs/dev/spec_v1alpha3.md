- [Основные положения](#основные-положения)
  - [Схема именования акторов](#схема-именования-акторов)
  - [Условное обозначение триггеров](#условное-обозначение-триггеров)
  - [Алгоритмы](#алгоритмы)
    - [Типы реплик и целевое количество реплик](#типы-реплик-и-целевое-количество-реплик)
  - [Константы](#константы)
    - [RVR Ready условия](#rvr-ready-условия)
    - [RV Ready условия](#rv-ready-условия)
    - [Алгоритмы хеширования shared secret](#алгоритмы-хеширования-shared-secret)
    - [Порты DRBD](#порты-drbd)
    - [Финализаторы ресурсов](#финализаторы-ресурсов)
- [Контракт данных: `ReplicatedVolume`](#контракт-данных-replicatedvolume)
  - [`spec`](#spec)
  - [`status`](#status)
- [Контракт данных: `ReplicatedVolumeReplica`](#контракт-данных-replicatedvolumereplica)
  - [`spec`](#spec-1)
  - [`status`](#status-1)
- [Акторы приложения: `agent`](#акторы-приложения-agent)
  - [`drbd-config-controller`](#drbd-config-controller)
    - [Статус: \[TBD | priority: 5 | complexity: 5\]](#статус-tbd--priority-5--complexity-5)
  - [`drbd-resize-controller`](#drbd-resize-controller)
    - [Статус: \[OK | priority: 5 | complexity: 2\]](#статус-ok--priority-5--complexity-2)
  - [`drbd-primary-controller`](#drbd-primary-controller)
    - [Статус: \[OK | priority: 5 | complexity: 2\]](#статус-ok--priority-5--complexity-2-1)
  - [`rvr-drbd-status-controller`](#rvr-drbd-status-controller)
  - [`rvr-status-config-address-controller`](#rvr-status-config-address-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3)
- [Акторы приложения: `controller`](#акторы-приложения-controller)
  - [`rvr-diskful-count-controller`](#rvr-diskful-count-controller)
    - [Статус: \[OK | priority: 5 | complexity: 4\]](#статус-ok--priority-5--complexity-4)
  - [`rvr-scheduling-controller`](#rvr-scheduling-controller)
    - [Статус: \[OK | priority: 5 | complexity: 5\]](#статус-ok--priority-5--complexity-5-1)
  - [`rvr-status-config-node-id-controller`](#rvr-status-config-node-id-controller)
    - [Статус: \[OK | priority: 5 | complexity: 2\]](#статус-ok--priority-5--complexity-2-2)
  - [`rvr-status-config-peers-controller`](#rvr-status-config-peers-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3-1)
  - [`rv-status-config-device-minor-controller`](#rv-status-config-device-minor-controller)
    - [Статус: \[OK | priority: 5 | complexity: 2\]](#статус-ok--priority-5--complexity-2-3)
  - [`rvr-tie-breaker-count-controller`](#rvr-tie-breaker-count-controller)
    - [Статус: \[OK | priority: 5 | complexity: 4\]](#статус-ok--priority-5--complexity-4-1)
  - [`rvr-access-count-controller`](#rvr-access-count-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3-2)
  - [`rv-publish-controller`](#rv-publish-controller)
    - [Статус: \[OK | priority: 5 | complexity: 4\]](#статус-ok--priority-5--complexity-4-2)
  - [`rvr-volume-controller`](#rvr-volume-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3-3)
  - [`rvr-gc-controller`](#rvr-gc-controller)
    - [Статус: \[TBD | priority: 5 | complexity: 2\]](#статус-tbd--priority-5--complexity-2-2)
    - [Контекст](#контекст)
  - [`rvr-owner-reference-controller`](#rvr-owner-reference-controller)
    - [Статус: \[OK | priority: 5 | complexity: 1\]](#статус-ok--priority-5--complexity-1)
  - [`rv-status-config-quorum-controller`](#rv-status-config-quorum-controller)
    - [Статус: \[OK | priority: 5 | complexity: 4\]](#статус-ok--priority-5--complexity-4-3)
  - [`rv-status-config-shared-secret-controller`](#rv-status-config-shared-secret-controller)
    - [Статус: \[OK | priority: 3 | complexity: 3\]](#статус-ok--priority-3--complexity-3)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller)
  - [`rvr-node-cordon-controller`](#rvr-node-cordon-controller)
  - [`rvr-status-conditions-controller`](#rvr-status-conditions-controller)
    - [Статус: \[TBD | priority: 5 | complexity: 2\]](#статус-tbd--priority-5--complexity-2-3)

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

## Алгоритмы

### Типы реплик и целевое количество реплик

Существуют три вида реплик по предназначению:
 - \[DF\] diskful - чтобы воспользоваться диском
 - \[DL-AP\] diskless (access point) - чтобы воспользоваться быстрым доступом к данным, не ожидая долгой синхронизации, либо при отсутствии диска
 - \[DL-TB\] diskless (tie-breaker) - чтобы участвовать в кворуме при чётном количестве других реплик

В зависимости от значения свойства `ReplicatedStorageClass` `spec.replication`, количество реплик разных типов следующее:
 - `None`
   - \[DF\]: 1
   - \[DL-AP\]: 0 / 1 / 2
 - `Availability`
   - \[DF\]: 2
   - \[DL-TB\]: 1 / 0 / 1
   - \[DL-AP\]: 0 / 1 / 2
 - `ConsistencyAndAvailability`
   - \[DF\]: 3
   - \[DL-AP\]: 1

Для миграции надо две primary.

Виртуалка может подключится к TB либо запросить себе AP. 

В случае если `spec.volumeAcess!=Local` AP не может быть Primary.

TB в любой ситуации поддерживает нечетное, и сама может превратится в AP. Превращение происходит с помощью удаления.

TODO

## Константы
Константы - это значения, которые должны быть определены в коде во время компиляции программы.

Ссылка на константы в данной спецификации означает необходимость явного определения, либо переиспользования данной константы в коде.

### RVR Ready условия
Это список предикатов вида `rvr.status.conditions[type=<key>].status=<value>`, объединение которых является критерием
для выставления значения `rvr.status.conditions[type=Ready].status=True`.
 - `InitialSync==True`
 - `DevicesReady==True`
 - `ConfigurationAdjusted==True`
 - `Quorum==True`
 - `DiskIOSuspended==False`
 - `AddressConfigured==True`

### RV Ready условия
Это список предикатов вида `rv.status.conditions[type=<key>].status=<value>`, объединение которых является критерием
для выставления значения `rv.status.conditions[type=Ready].status=True`.
 - `QuorumConfigured==True`
 - `DiskfulReplicaCountReached==True`
 - `AllReplicasReady==True`
 - `SharedSecretAlgorithmSelected==True`

### Алгоритмы хеширования shared secret
 - `sha256`
 - `sha1`

### Порты DRBD
 - `drbdMinPort=7000` - минимальный порт для использования ресурсами 
 - `drbdMaxPort=7999` - максимальный порт для использования ресурсами

### Финализаторы ресурсов
- `rv`
  - `sds-replicated-volume.storage.deckhouse.io/controller`
- `rvr`
  - `sds-replicated-volume.storage.deckhouse.io/controller`
  - `sds-replicated-volume.storage.deckhouse.io/agent`
  - `sds-replicated-volume.storage.deckhouse.io/peers` TODO
  - `sds-replicated-volume.storage.deckhouse.io/quorum` TODO
- `llv`
  - `sds-replicated-volume.storage.deckhouse.io/controller`

# Контракт данных: `ReplicatedVolume`
## `spec`
- `size`
  - Тип: Kubernetes `resource.Quantity`.
  - Обязательное поле.
- `replicatedStorageClassName`
  - Обязательное поле.
  - Используется:
    - **rvr-diskful-count-controller** — определяет целевое число реплик по `ReplicatedStorageClass`.
    - **rv-publish-controller** — проверяет `rsc.spec.volumeAccess==Local` для возможности локального доступа.
- `publishOn[]`
  - До 2 узлов (MaxItems=2).
  - Используется:
    - **rv-publish-controller** — промоут/демоут реплик.
    - **rvr-access-count-controller** — поддержание количества `Access`-реплик.

## `status`
- `conditions[]`
  - `type=Ready`
    - Обновляется: **rv-status-controller**.
    - Критерий: все [RV Ready условия](#rv-ready-условия) достигнуты.
  - `type=QuorumConfigured`
    - Обновляется: **rv-status-config-quorum-controller**.
  - `type=SharedSecretAlgorithmSelected`
    - Обновляется: **rv-status-config-shared-secret-controller**.
    - При исчерпании вариантов: `status=False`, `reason=UnableToSelectSharedSecretAlgorithm`, `message=<node, alg>`.
  - `type=PublishSucceeded`
    - Обновляется: **rv-publish-controller**.
    - При невозможности локального доступа: `status=False`, `reason=UnableToProvideLocalVolumeAccess`, `message=<пояснение>`.
  - `type=DiskfulReplicaCountReached`
    - Обновляется: **rvr-diskful-count-controller**.
- `drbd.config`
  - Путь в API: `status.drbd.config.*`.
  - `sharedSecret`
    - Инициализирует: **rv-status-config-controller**.
    - Меняет при ошибке алгоритма на реплике: **rv-status-config-shared-secret-controller**.
  - `sharedSecretAlg`
    - Выбирает/обновляет: **rv-status-config-controller** / **rv-status-config-shared-secret-controller**.
  - `quorum`
    - Обновляет: **rv-status-config-quorum-controller** (см. формулу в описании контроллера).
  - `quorumMinimumRedundancy`
    - Обновляет: **rv-status-config-quorum-controller**.
  - `allowTwoPrimaries`
    - Обновляет: **rv-publish-controller** (включает при 2 узлах в `spec.publishOn`, выключает иначе).
  - `deviceMinor`
    - Обновляет: **rv-status-config-device-minor-controller** (уникален среди всех RV).
- `publishedOn[]`
  - Обновляется: **rv-publish-controller**.
  - Значение: список узлов, где `rvr.status.drbd.status.role==Primary`.
- `actualSize`
  - Присутствует в API; источник обновления не описан в спецификации.
- `phase`
  - Возможные значения: `Terminating`, `Synchronizing`, `Ready`.
  - Обновляется: **rv-status-controller**.

# Контракт данных: `ReplicatedVolumeReplica`
## `spec`
- `replicatedVolumeName`
  - Обязательное; неизменяемое.
  - Используется всеми контроллерами для привязки RVR к соответствующему RV.
- `nodeName`
  - Обновляется/используется: **rvr-scheduling-controller**.
  - Учитывается: **rvr-missing-node-controller**, **rvr-node-cordon-controller**.
- `type` (Enum: `Diskful` | `Access` | `TieBreaker`)
  - Устанавливается/меняется:
    - **rvr-diskful-count-controller** — создаёт `Diskful`.
    - **rvr-access-count-controller** — создаёт/переводит `TieBreaker→Access`, удаляет лишние `Access`.
    - **rvr-tie-breaker-count-controller** — создаёт/удаляет `TieBreaker`.

## `status`
- `conditions[]`
  - `type=Ready`
    - `reason`: `WaitingForInitialSync`, `DevicesAreNotReady`, `AdjustmentFailed`, `NoQuorum`, `DiskIOSuspended`, `Ready`.
  - `type=InitialSync`
    - `reason`: `InitialSyncRequiredButNotReady`, `SafeForInitialSync`, `InitialDeviceReadinessReached`.
  - `type=Primary`
    - `reason`: `ResourceRoleIsPrimary`, `ResourceRoleIsNotPrimary`.
  - `type=DevicesReady`
    - `reason`: `DeviceIsNotReady`, `DeviceIsReady`.
  - `type=ConfigurationAdjusted`
    - `reason`: `ConfigurationFailed`, `MetadataCheckFailed`, `MetadataCreationFailed`, `StatusCheckFailed`, `ResourceUpFailed`, `ConfigurationAdjustFailed`, `ConfigurationAdjustmentPausedUntilInitialSync`, `PromotionDemotionFailed`, `ConfigurationAdjustmentSucceeded`.
    - Примечание: `reason=UnsupportedAlgorithm` упомянут в спецификации, но отсутствует среди API-констант.
  - `type=Quorum`
    - `reason`: `NoQuorumStatus`, `QuorumStatus`.
  - `type=DiskIOSuspended`
    - `reason`: `DiskIONotSuspendedStatus`, `DiskIOSuspendedUnknownReason`, `DiskIOSuspendedByUser`, `DiskIOSuspendedNoData`, `DiskIOSuspendedFencing`, `DiskIOSuspendedQuorum`.
- `actualType` (Enum: `Diskful` | `Access` | `TieBreaker`)
  - Обновляется контроллерами; используется **rvr-volume-controller** для удаления LLV при `spec.type!=Diskful` только когда `actualType==spec.type`.
- `drbd.config`
  - `nodeId` (0..7)
    - Обновляет: **rvr-status-config-node-id-controller** (уникален в пределах RV).
  - `address.ipv4`, `address.port`
    - Обновляет: **rvr-status-config-address-controller**; IPv4; порт ∈ [1025;65535]; выбирается свободный порт DRBD.
  - `peers`
    - Обновляет: **rvr-status-config-peers-controller** — у каждой готовой RVR перечислены все остальные готовые реплики того же RV.
  - `disk`
    - Обеспечивает: **rvr-volume-controller** при `spec.type==Diskful`; формат `/dev/<VG>/<LV>`.
  - `primary`
    - Обновляет: **rv-publish-controller** (промоут/демоут).
- `drbd.actual`
  - `allowTwoPrimaries`
    - Используется: **rv-publish-controller** (ожидание применения настройки на каждой RVR).
  - `disk`
    - Поле присутствует в API; не используется в спецификации явно.
- `drbd.status`
  - Публикуется: **rvr-drbd-status-controller**; далее используется другими контроллерами.
  - `name`, `nodeId`, `role`, `suspended`, `suspendedUser`, `suspendedNoData`, `suspendedFencing`, `suspendedQuorum`, `forceIOFailures`, `writeOrdering`.
  - `devices[]`: `volume`, `minor`, `diskState`, `client`, `open`, `quorum`, `size`, `read`, `written`, `alWrites`, `bmWrites`, `upperPending`, `lowerPending`.
  - `connections[]`: `peerNodeId`, `name`, `connectionState`, `congested`, `peerRole`, `tls`, `apInFlight`, `rsInFlight`,
    - `paths[]`: `thisHost.address`, `thisHost.port`, `thisHost.family`, `remoteHost.address`, `remoteHost.port`, `remoteHost.family`, `established`,
    - `peerDevices[]`: `volume`, `replicationState`, `peerDiskState`, `peerClient`, `resyncSuspended`, `outOfSync`, `pending`, `unacked`, `hasSyncDetails`, `hasOnlineVerifyDetails`, `percentInSync`.

Поля API, не упомянутые в спецификации:
- `status.drbd.actual.disk`.

# Акторы приложения: `agent`

## `drbd-config-controller`

### Статус: [OK | priority: 5 | complexity: 5]

### Цель 

Согласовать желаемую конфигурацию в полях ресурсов и конфигурации DRBD, выполнять
первоначальную синхронизацию и настройку DRBD ресурсов на ноде. Название ноды 
`rvr.spec.nodeName` должно соответствовать названию ноды контроллера
(переменная окружения `NODE_NAME`, см. `images/agent/cmd/env_config.go`)

Обязательные поля. Нельзя приступать к конфигурации, пока значение поля не
проинициализировано:
- `rv.metadata.name`
- `rv.status.drbd.config.sharedSecret`
- `rv.status.drbd.config.sharedSecretAlg`
- `rv.status.drbd.config.deviceMinor`
- `rvr.status.drbd.config.nodeId`
- `rvr.status.drbd.config.address`
- `rvr.status.drbd.config.peers`
  - признак инициализации: `rvr.status.drbd.config.peersInitialized`
- `rvr.status.lvmLogicalVolumeName`
  - обязателен только для `rvr.spec.type=Diskful`

Дополнительные поля. Можно приступать к конфигурации с любыми значениями в них:
- `rv.status.drbd.config.quorum`
- `rv.status.drbd.config.quorumMinimumRedundancy`
- `rv.status.drbd.config.allowTwoPrimaries`

Список ошибок, которые требуется поддерживать (выставлять и снимать) после каждого
реконсайла:
  - `rvr.status.drbd.errors.sharedSecretAlgSelectionError` - результат валидации алгоритма
  - `rvr.status.drbd.errors.lastAdjustmentError` - вывод команды `drbdadm adjust`
  - `rvr.status.drbd.errors.<...>Error` - вывод любой другой использованной команды `drbd` (требуется доработать API контракт)

Список полей, которые требуется поддерживать (выставлять и снимать) как результат каждого
реконсайла:
  - `rvr.status.drbd.actual.disk` - должно соответствовать пути к диску `rvr.status.lvmLogicalVolumeName`
    - только для `rvr.spec.type==Diskful`
    - формат `/dev/{actualVGNameOnTheNode}/{actualLVNameOnTheNode}`
  - `rvr.status.drbd.actual.allowTwoPrimaries` - должно соответствовать `rv.status.drbd.config.allowTwoPrimaries`
  - `rvr.status.drbd.actual.initialSyncCompleted`

Для работы с форматом конфигурации DRBD предлагается воспользоваться существующими пакетами
 - см. метод `writeResourceConfig` в `images/agent/internal/reconcile/rvr/reconcile_handler.go`.
Также требуется использовать те же самые параметры по умолчанию (`protocol`, `rr-conflict`, и т.д.)

Существующая реализация поддерживает `Diskful` и `Access` типы реплик. Для
`TieBreaker` реплик требуется изменить параметры так, чтобы избежать
синхронизации метаданных на ноду (провести исследование самостоятельно).

Последовательность реконсайла, если не заполнен `rvr.metadata.deletionTimestamp`:
- ставим финализаторы на rvr
  - `sds-replicated-volume.storage.deckhouse.io/agent`
  - `sds-replicated-volume.storage.deckhouse.io/controller`
- пишем конфиг во временный файл и проверяем валидность
  - команда (новая, нужно реализовать аналогично другим): `drbdadm --config-to-test <...>.res_tmp --config-to-exclude <...>.res sh-nop`
  - в случае невалидного конфига, нужно вывести ошибку в `rvr.status.drbd.errors.<...>` и прекратить реконсайл
- пишем конфиг в основной файл (можно переместить, либо пересоздать и удалить временный)
- если `rvr.spec.type==Diskful`
  - проверяем наличие метаданных
    - `drbdadm dump-md`
      - см. существующую реализацию
    - если метаданных нет - создаем их
      - `drbdadm create-md`
        - см. существующую реализацию
  - проверяем необходимость первоначальной синхронизации (AND)
    - `rvr.status.drbd.config.peersInitialized`
    - `len(rvr.status.drbd.config.peers)==0`
    - `rvr.status.drbd.status.devices[0].diskState != UpToDate`
    - `rvr.status.drbd.actual.initialSyncCompleted!=true`
  - если первоначальная синхронизация нужна
    - выполняем `drdbadm primary --force`
      - см. существующую реализацию
    - выполняем `drdbadm secondary`
      - см. существующую реализацию
  - выставляем `rvr.status.drbd.actual.initialSyncCompleted=true`
- если `rvr.spec.type!=Diskful`
  - выставляем `rvr.status.drbd.actual.initialSyncCompleted=true`
- выполнить `drbdadm status`, чтобы убедиться, не "поднят" ли ресурс
  - см. существующую реализацию
- если ресурс "не поднят", выполнить `drbdadm up`
  - см. существующую реализацию
- выполнить `drbdadm adjust`
  - см. существующую реализацию

Если заполнен `rvr.metadata.deletionTimestamp`:
- если есть другие финализаторы, кроме `sds-replicated-volume.storage.deckhouse.io/agent`,
то прекращаем реконсайл, т.к. агент должен быть последним, кто удаляет свой финализатор
- выполнить `drbdadm down`
  - см. существующую реализацию
- удалить конфиги ресурса (основной и временный), если они есть
- снять последний финализатор с rvr

### Вывод 
  - `rvr.status.drbd.errors.*`
  - `rvr.status.drbd.actual.*`
  - *.res, *.res_tmp файлы на ноде

## `drbd-resize-controller`

### Статус: [OK | priority: 5 | complexity: 2]

### Цель
Выполнить команду `drbdadm resize`, когда желаемый размер диска больше
фактического.

Команда должна выполняться на `rvr.spec.type=Diskful` ноде с наименьшим
`rvr.status.drbd.config.nodeId` для ресурса.

Cм. существующую реализацию `drbdadm resize`.

Предусловия для выполнения команды (AND):
  - `rv.status.conditions[type=Ready].status=True`
  - `rvr.status.drbd.initialSyncCompleted=true`
  - `rv.status.actualSize != nil`
  - `rv.size - rv.status.actualSize > 0`

Поле `rv.status.actualSize` должно поддерживаться актуальным размером. Когда оно
незадано - его требуется задать. После успешного изменения размера тома - его
требуется обновить.

Ошибки drbd команд требуется выводить в `rvr.status.drbd.errors.*`.

### Вывод 
 - `rvr.status.drbd.errors.*`
 - `rv.status.actualSize`

## `drbd-primary-controller`

### Статус: [OK | priority: 5 | complexity: 2]

### Цель
Выполнить команду `drbdadm primary`/`drbdadm secondary`, когда желаемая роль ресурса не
соответствует фактической.

Команда должна выполняться на `rvr.spec.nodeName` ноде.

Cм. существующую реализацию `drbdadm primary` и `drbdadm secondary`.

Предусловия для выполнения команды (AND):
  - `rv.status.conditions[type=Ready].status=True`
  - `rvr.status.drbd.initialSyncCompleted=true`
  - OR
    - выполняем `drbdadm primary` (AND)
      - `rvr.status.drbd.config.primary==true`
      - `rvr.status.drbd.status.role==Primary`
    - выполняем `drbdadm secondary` (AND)
      - `rvr.status.drbd.config.primary==false`
      - `rvr.status.drbd.status.role!=Primary`

Ошибки drbd команд требуется выводить в `rvr.status.drbd.errors.*`.

### Вывод 
  - `rvr.status.drbd.errors.*`

## `rvr-drbd-status-controller`

### Цель 

### Триггер 
  - 
### Вывод 
  - 

## `rvr-status-config-address-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель 
Проставить значение свойству `rvr.status.drbd.config.address`.
 - `ipv4` - взять из `node.status.addresses[type=InternalIP]`
 - `port` - найти наименьший свободный порт в диапазоне, задаваемом в [портах DRBD](#Порты-DRBD) `drbdMinPort`/`drbdMaxPort`

В случае, если нет свободного порта, настроек порта, либо IP: повторять реконсайл с ошибкой.

Процесс и результат работы контроллера должен быть отражён в `rvr.status.conditions[type=AddressConfigured]`

### Триггер 
  - `CREATE/UPDATE(RVR, rvr.spec.nodeName, !rvr.status.drbd.config.address)`

### Вывод 
  - `rvr.status.drbd.config.address`
  - `rvr.status.conditions[type=AddressConfigured]`

# Акторы приложения: `controller`

## `rvr-diskful-count-controller`

### Статус: [OK | priority: 5 | complexity: 4]

### Цель 
Добавлять привязанные diskful-реплики (RVR) для RV.

Целевое количество реплик определяется в `ReplicatedStorageClass` (получать через `rv.spec.replicatedStorageClassName`).

Первая реплика должна перейти в полностью работоспособное состояние, прежде чем
будет создана вторая реплика. Вторая и последующие реплики могут быть созданы
параллельно.

Процесс и результат работы контроллера должен быть отражён в `rv.status.conditions[type=DiskfulReplicaCountReached]`

### Триггер
  - `CREATE(RV)`, `UPDATE(RVR[metadata.deletionTimestamp -> !null])`
    - когда фактическое количество реплик (в том числе неработоспособных, но исключая удаляемые) меньше требуемого
  - `UPDATE(RVR[status.conditions[type=Ready].status == True])`
    - когда фактическое количество реплик равно 1

### Вывод
  - создаёт diskful RVR (`rvr.spec.type==Diskful`) вплоть до RV->
[RSC->`spec.replication`](https://deckhouse.io/modules/sds-replicated-volume/stable/cr.html#replicatedstorageclass-v1alpha1-spec-replication)
    - `spec.replicatedVolumeName` имеет значение RV `metadata.name`
    - `metadata.ownerReferences` указывает на RV по имени `metadata.name`
    - `rv.status.conditions[type=DiskfulReplicaCountReached]`

## `rvr-scheduling-controller`

### Статус: [OK | priority: 5 | complexity: 5]

### Цель

Назначить всем rvr каждой rv уникальную ноду, задав поле `rvr.spec.nodeName`.
Список нод определяется пересечением нод из двух наборов:
- ноды находящиеся в зонах `rsc.spec.zones`. Если там ничего не указано - все ноды. Если тип `Access` - то все ноды.
- ноды, на которых размещены LVG `rsp.spec.lvmVolumeGroups` (применимо только для `Diskful` нод, иначе - все ноды)

Четыре последовательные фазы:

- Размещение `Diskful` & `Local` (`rsc.spec.volumeAccess==Local`)
  - фаза работает только если `rsc.spec.volumeAccess==Local`
  - фаза работает только если `rv.spec.publishOn` задан и не на всех нодах из `rv.spec.publishOn` есть реплики
  - берём оставшиеся узлы из `rv.spec.publishOn` и пытаемся на них разместить реплики
  - исключаем из планирования узлы, на которых уже есть реплики этой RV (любого типа)
  - учитываем topology
    - `Zonal` - все реплики должны быть в рамках одной зоны
    - `TransZonal` - все реплики должны быть в разных зонах
    - `Any` - зоны не учитываются, реплики размещаются по произвольным нодам
  - учитываем место
    - делаем вызов в scheduler-extender (см. https://github.com/deckhouse/sds-node-configurator/pull/183)
  - если хотя бы на одну ноду из `rv.spec.publishOn` не удалось разместить реплику - ошибка невозможности планирования
- Размещение `Diskful` (не `Local`)
  - исключаем из планирования узлы, на которых уже есть реплики этой RV (любого типа)
  - учитываем topology
    - `Zonal` - все реплики должны быть в рамках одной зоны
    - `TransZonal` - все реплики должны быть в разных зонах
    - `Any` - зоны не учитываются, реплики размещаются по произвольным нодам
  - учитываем место
    - делаем вызов в scheduler-extender (см. https://github.com/deckhouse/sds-node-configurator/pull/183)
  - пытаемся учесть `rv.spec.publishOn` - назначить `Diskful` реплики на эти ноды, если это возможно (увеличиваем приоритет таких нод)
- Размещение `Access`
  - фаза работает только если `rv.spec.publishOn` задан и не на всех нодах из `rv.spec.publishOn` есть реплики
  - исключаем из планирования узлы, на которых уже есть реплики этой RV (любого типа)
  - не учитываем topology, место на диске
  - допустимо иметь ноды в `rv.spec.publishOn`, на которые не хватило реплик
  - допустимо иметь реплики, которые никуда не запланировались (потому что на всех `rv.spec.publishOn` и так есть
  реплики какого-то типа)
- Размещение `TieBreaker`
  - исключаем из планирования узлы, на которых уже есть реплики этой RV (любого типа)
    - для `rsc.spec.topology=Zonal`
      - исключаем узлы из других зон
  - для `rsc.spec.topology=TransZonal`
    - каждый rvr планируем в зону с самым маленьким количеством реплик
    - если зон с самым маленьким количество несколько - то в любую из них
    - если в зонах с самым маленьким количеством реплик нет ни одного свободного узла - 
    ошибка невозможности планирования

Ошибка невозможности планирования:
  - в каждой rvr протавляем
    - `rvr.status.conditions[type=Scheduled].status=False`
    - `rvr.status.conditions[type=Scheduled].reason=`
      - `<исходя из сценария неудачи>`
      - `WaitingForAnotherReplica` - для rvr, к размещению которых ещё не приступали
    - `rvr.status.conditions[type=Scheduled].message=<для пользователя>`

В случае успешного планирования:
  - в rvr протавляем
    - `rvr.status.conditions[type=Scheduled].status=True`
    - `rvr.status.conditions[type=Scheduled].reason=ReplicaScheduled`

### Вывод
  - `rvr.spec.nodeName`
  - `rvr.status.conditions[type=Scheduled]`

## `rvr-status-config-node-id-controller`

### Статус: [OK | priority: 5 | complexity: 2]

### Цель
Проставить свойству `rvr.status.drbd.config.nodeId` уникальное значение среди всех реплик одной RV, в диапазоне [0; 7].

В случае превышения количества реплик, повторять реконсайл с ошибкой.

### Триггер
  - `CREATE(RVR, status.drbd.config.nodeId==nil)`

### Вывод
  - `rvr.status.drbd.config.nodeId`

## `rvr-status-config-peers-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель
Поддерживать актуальное состояние пиров на каждой реплике.

Для любого RV у всех готовых RVR в пирах прописаны все остальные готовые, кроме неё.

Готовая RVR - та, у которой `spec.nodeName!="", status.nodeId !=nil, status.address != nil`

После первой инициализации, даже в случае отсутствия пиров, требуется поставить
`rvr.status.drbd.config.peersInitialized=true` в том же патче.

### Вывод
  - `rvr.status.drbd.config.peers`
  - `rvr.status.drbd.config.peersInitialized`

## `rv-status-config-device-minor-controller`

### Статус: [OK | priority: 5 | complexity: 2]

### Цель

Инициализировать свойство `rv.status.drbd.config.deviceMinor` минимальным свободным значением среди всех RV.

По завершению работы контроллера у каждой RV должен быть свой уникальный `rv.status.drbd.config.deviceMinor`.

### Триггер
  - `CREATE/UPDATE(RV, rv.status.drbd.config.deviceMinor != nil)`

### Вывод
  - `rv.status.drbd.config.deviceMinor`

## `rvr-tie-breaker-count-controller`

### Статус: [OK | priority: 5 | complexity: 4]

### Цель

Failure domain (FD) - либо - нода, либо, в случае, если `rsc.spec.topology==TransZonal`, то - и нода, и зона.

Создавать и удалять RVR с `rvr.spec.type==TieBreaker`, чтобы поддерживались требования: 

- отказ любого одного FD не должен приводить к потере кворума
- отказ большинства FD должен приводить к потере кворума
- поэтому надо тай-брейкерами доводить количество реплик на всех FD до минимального
числа, при котором будут соблюдаться условия:
  -  отличие в количестве реплик между FD не больше чем на 1
  -  общее количество реплик - нечётное


### Вывод
  - Новая rvr с `rvr.spec.type==TieBreaker`
  - `rvr.metadata.deletionTimestamp==true`

## `rvr-access-count-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель 
Поддерживать количество `rvr.spec.type==Access` реплик (для всех режимов
`rsc.spec.volumeAccess`, кроме `Local`) таким, чтобы их хватало для размещения на тех узлах, где это требуется:
 - список запрашиваемых для доступа узлов обновляется в `rv.spec.publishOn`
 - `Access` реплики требуются для доступа к данным на тех узлах, где нет других реплик
Когда узел больше не в `rv.spec.publishOn`, а также не в `rv.status.publishedOn`,
`Access` реплика на нём должна быть удалена.

### Вывод
  - создает, обновляет, удаляет `rvr`

## `rv-publish-controller`

### Статус: [OK | priority: 5 | complexity: 4]

### Цель 

Обеспечить переход в primary (промоут) и обратно реплик. Для этого нужно следить за списком нод в запросе на публикацию `rv.spec.publishOn` и приводить в соответствие реплики на этой ноде, проставляя им `rvr.status.drbd.config.primary`. 

В случае, если `rsc.spec.volumeAccess==Local`, но реплика не `rvr.spec.type==Diskful`,
либо её нет вообще, промоут невозможен, и требуется обновить rvr и прекратить реконсайл:
   - `rv.status.conditions[type=PublishSucceeded].status=False`
   - `rv.status.conditions[type=PublishSucceeded].reason=UnableToProvideLocalVolumeAccess`
   - `rv.status.conditions[type=PublishSucceeded].message=<сообщение для пользователя>`

Не все реплики могут быть primary. Для `rvr.spec.type=TieBreaker` требуется поменять тип на
`rvr.spec.type=Accees` (в одном патче вместе с `rvr.status.drbd.config.primary`).

В `rv.spec.publishOn` может быть указано 2 узла. Однако, в кластере по умолчанию стоит запрет на 2 primary ноды. В таком случае, нужно временно выключить запрет:
 - поменяв `rv.status.drbd.config.allowTwoPrimaries=true`
 - дождаться фактического применения настройки на каждой rvr `rvr.status.drbd.actual.allowTwoPrimaries`
 - и только потом обновлять `rvr.status.drbd.config.primary`

В случае, когда в `rv.spec.publishOn` менее двух нод, нужно убедиться, что настройка `rv.status.drbd.config.allowTwoPrimaries=false`.

Также требуется поддерживать свойство `rv.status.publishedOn`, указывая там список нод, на которых
фактически произошёл переход реплики в состояние Primary. Это состояние публикуется в `rvr.status.drbd.status.role` (значение `Primary`).

Контроллер работает только когда RV имеет `status.condition[type=Ready].status=True`

### Вывод 
  - `rvr.status.drbd.config.primary`
  - `rv.status.drbd.config.allowTwoPrimaries`
  - `rv.status.publishedOn`
  - `rv.status.conditions[type=PublishSucceeded]`

## `rvr-volume-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Цель
1. Обеспечить наличие LLV для каждой реплики, у которой
   - `rvr.spec.type==Diskful`
   - `rvr.metadata.deletionTimestamp==nil`
  Всем LLV под управлением проставляется `metadata.ownerReference`, указывающий на RVR.
2. Обеспечить проставление значения в свойства `rvr.status.lvmLogicalVolumeName`, указывающее на соответствующую LLV, готовую к использованию.
3. Обеспечить отсутствие LLV диска у RVR с `rvr.spec.type!=Diskful`, но только когда
фактический тип (`rvr.status.actualType`) соответствует целевому `rvr.spec.type`.
4. Обеспечить сброс свойства `rvr.status.lvmLogicalVolumeName` после удаления LLV.

### Вывод 
  - Новое `llv`
  - Обновление для уже существующих: `llv.metadata.ownerReference`
  - `rvr.status.lvmLogicalVolumeName` (задание и сброс)

## `rvr-gc-controller`

### Статус: [TBD | priority: 5 | complexity: 2]

### Контекст

Приложение agent ставит 2 финализатора на все RVR до того, как сконфигурирует DRBD.
  - `sds-replicated-volume.storage.deckhouse.io/agent` (далее - `F/agent`)
  - `sds-replicated-volume.storage.deckhouse.io/controller` (далее - `F/controller`)

При удалении RVR, agent не удаляет ресурс из DRBD, и не снимает финализаторы,
пока стоит `F/controller`.

### Цель 

Цель `rvr-gc-controller` - снимать финализатор `F/controller` с удаляемых rvr, когда 
кластер к этому готов. Условия готовности:

- количество rvr `rvr.status.conditions[type=Ready].status == rvr.status.conditions[type=FullyConnected].status == True`
(исключая ту, которую собираются удалить) больше, либо равно `rv.status.drbd.config.quorum`
- присутствует необходимое количество `rvr.status.actualType==Diskful && rvr.status.conditions[type=Ready].status==True && rvr.metadata.deletionTimestamp==nil` реплик, в
соответствии с `rsc.spec.replication`
- удаляемая реплика не является фактически опубликованной, т.е. её нода не в `rv.status.publishedOn`


### Вывод
 - удалить `rvr.metadata.finalizers[sds-replicated-volume.storage.deckhouse.io/controller]`

## `rvr-owner-reference-controller`

### Статус: [OK | priority: 5 | complexity: 1]

### Цель 

Поддерживать `rvr.metada.ownerReference`, указывающий на `rv` по имени
`rvr.spec.replicatedVolumeName`.

Чтобы выставить правильные настройки, требуется использовать функцию `SetControllerReference` из пакета
`sigs.k8s.io/controller-runtime/pkg/controller/controllerutil`.

### Вывод 
 - `rvr.metada.ownerReference`

## `rv-status-config-quorum-controller`

### Статус: [OK | priority: 5 | complexity: 4]

### Цель 

Поднять значение кворума до необходимого, после того как кластер станет работоспособным.

Работоспособный кластер - это RV, у которого все [RV Ready условия](#rv-ready-условия) достигнуты, без учёта условия `QuorumConfigured`.

До поднятия кворума нужно поставить финализатор на каждую RVR. Также необходимо обработать проставление rvr.`metadata.deletionTimestamp` таким образом, чтобы финализатор с RVR был снят после фактического уменьшения кворума.

Процесс и результат работы контроллера должен быть отражён в `rv.status.conditions[type=QuorumConfigured]`

### Триггер
 - `CREATE/UPDATE(RV, rv.status.conditions[type=Ready].status==True)`

### Вывод
  - `rv.status.drbd.config.quorum`
  - `rv.status.drbd.config.quorumMinimumRedundancy`
  - `rv.status.conditions[type=QuorumConfigured]`

Правильные значения:

N - все реплики
M - diskful реплики

```
if M > 1 {
  var quorum byte = max(2, N/2 + 1)
  var qmr byte = max(2, M/2 +1)
} else {
  var quorum byte = 0
  var qmr byte = 0
}
```

## `rv-status-config-shared-secret-controller`

### Статус: [OK | priority: 3 | complexity: 3]

### Цель
Проставить первоначальное значения для `rv.status.drbd.config.sharedSecret` и `rv.status.drbd.config.sharedSecretAlg`,
а также обработать ошибку применения алгоритма на любой из реплик из `rvr.status.drbd.errors.sharedSecretAlgSelectionError`, и поменять его на следующий по [списку алгоритмов хеширования](Алгоритмы хеширования shared secret). Последний проверенный алгоритм должен быть указан в `rvr.status.drbd.errors.sharedSecretAlgSelectionError.unsupportedAlg`.

В случае, если список закончился - прекратить попытки.

### Триггер
 - `CREATE(RV)`
 - `CREATE/UPDATE(RVR)`

### Вывод 
 - `rv.status.drbd.config.sharedSecret`
   - генерируется новый
 - `rv.status.drbd.config.sharedSecretAlg`
   - выбирается из захардкоженного списка по порядку

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

## `rvr-status-conditions-controller`

### Статус: [TBD | priority: 5 | complexity: 2]

### Цель

Поддерживать вычисляемые поля для отображения пользователю.

- `rvr.status.conditions[type=<>]`
  - `Quorum`
    - `status`
      - `True`
        - `rvr.status.drbd.status.devices[0].quorum=true`
      - `False` - иначе
        - `reason` - в соответствии с причиной
  - `InSync`
    - `status`
      - `True`
        - `rvr.status.drbd.status.devices[0].diskState=UpToDate`
      - `False` - иначе
        - `reason` - в соответствии с причиной
  - `Scheduled` - управляется `rvr-scheduling-controller`, не менять
  - `Configured`
    - `status`
      - `True` (AND)
        - если все поля в `rvr.status.drbd.actual.*` равны соответствующим
      полям-источникам в `rv.status.drbd.config` или `rvr.status.drbd.config`
        - `rvr.status.drbd.errors.lastAdjustmentError == nil`
        - `rvr.status.drbd.errors.lastPromotionError == nil`
        - `rvr.status.drbd.errors.lastResizeError == nil`
        - `rvr.status.drbd.errors.<...>Error == nil`
      - `False` - иначе
        - `reason` - в соответствии с причиной
        - `message` - сформировать из `rvr.status.drbd.errors.<...>Error`
  - `Ready`
    - `status`
      - `True` (AND)
        - `Quorum=True`
        - `InSync!=False`
        - `Scheduled=True`
        - `Configured=True`
      - `False` - инчае
        - `reason` - в соответствии с причиной
  - `VolumeAccessReady` - существует только для `Access` и `Diskful` реплик
    - `status`
      - `True` (AND)
        - `rvr.status.drbd.status.role==Primary`
        - нет проблем с I/O (см. константы `ReasonDiskIOSuspended<...>`)
        - `Quorum=True`
      - `False` - иначе
        - `reason`
          - `NotPublished` - если не Primary
          - `IOSuspendedByQuorum`
          - `IOSuspendedBy<...>` - (см. константы `ReasonDiskIOSuspended<...>`)
          - `IOSuspendedBySnapshotter` - добавить константу на будущее

TODO: коннекты между разными узлами
TODO: что ещё нужно для UI (%sync?)?
TODO: SharedSecretAlgorithmSelected .reason=UnableToSelectSharedSecretAlgorithm
TODO: AddressConfigured - мб заменить на `rvr.status.errors.<...>Error` ?

### Вывод 
  - `rvr.status.conditions`


## `rv-status-conditions-controller`

## `rv-gc-controller`

## `tie-breaker-removal-controller`

# Сценарии

## Отказоустойчивость
### Arrange
### Act
### Assert

## Нагрузочный
### Arrange
### Act
### Assert

