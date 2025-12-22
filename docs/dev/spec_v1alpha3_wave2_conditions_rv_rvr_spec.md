# Спецификация изменений Conditions (v1alpha3)

## Терминология

| Аббревиатура | Полное название | Описание |
|--------------|-----------------|----------|
| **RV** | ReplicatedVolume | Реплицируемый том |
| **RVR** | ReplicatedVolumeReplica | Реплика тома (одна копия на одной ноде) |
| **RSC** | ReplicatedStorageClass | Класс хранения для реплицируемых томов |
| **LLV** | LvmLogicalVolume | Реализация BackingVolume через LVM |

**Соглашения:**
- `rv.field` / `rvr.field` — ссылка на поле объекта (lowercase)
- `RV.Condition` / `RVR.Condition` — название условия (uppercase)

---

## Обзор: RVR Conditions

| Condition | Описание | Устанавливает | Reasons |
|-----------|----------|---------------|---------|
| `Scheduled` | Нода выбрана | rvr-scheduling-controller | `ReplicaScheduled`, `WaitingForAnotherReplica`, `NoAvailableNodes`, `TopologyConstraintsFailed`, `InsufficientStorage` |
| `BackingVolumeCreated` | BackingVolume создан и ready | rvr-volume-controller | `BackingVolumeReady`, `BackingVolumeNotReady`, `WaitingForBackingVolume`, `BackingVolumeCreationFailed`, `NotApplicable` |
| `Initialized` | Инициализация (не снимается) | drbd-config-controller (agent) | `Initialized`, `WaitingForInitialSync`, `InitialSyncInProgress` |
| `InQuorum` | Реплика в кворуме | drbd-status-controller (agent) | `InQuorum`, `QuorumLost` |
| `InSync` | Данные синхронизированы | drbd-status-controller (agent) | `InSync`, `Synchronizing`, `OutOfSync`, `Inconsistent`, `Diskless`, `DiskAttaching` |
| `Configured` | Конфигурация применена | drbd-config-controller (agent) | `Configured`, `ConfigurationPending`, `ConfigurationFailed`, ...errors... |
| `Online` | Scheduled + Initialized + InQuorum | rvr-status-conditions-controller | `Online`, `Unscheduled`, `Uninitialized`, `QuorumLost`, `NodeNotReady`, `AgentNotReady` |
| `IOReady` | Online + InSync (safe) | rvr-status-conditions-controller | `IOReady`, `Offline`, `OutOfSync`, `Synchronizing`, `NodeNotReady`, `AgentNotReady` |
| `Published` | Реплика Primary | rv-publish-controller | `Published`, `Unpublished`, `PublishPending` |
| `AddressConfigured` | Адрес DRBD настроен | rvr-status-config-address-controller (agent) | `AddressConfigured`, `WaitingForAddress` |

### Удаляемые

| Condition | Причина удаления |
|-----------|------------------|
| ~~`Ready`~~ | Неоднозначная семантика "готова к чему?". Заменён на `Online` + `IOReady`. |

---

## Обзор: RV Conditions

| Condition | Описание | Устанавливает | Reasons |
|-----------|----------|---------------|---------|
| ~~`QuorumConfigured`~~ | ~~Конфигурация кворума~~ | ❌ убрать | Дублирует `rv.status.drbd.config.quorum != nil` |
| ~~`DiskfulReplicaCountReached`~~ | ~~Кол-во Diskful достигнуто~~ | ❌ убрать | Дублирует счётчик `diskfulReplicaCount` |
| `Scheduled` | Все RVR Scheduled | rv-status-conditions-controller | `AllReplicasScheduled`, `ReplicasNotScheduled`, `SchedulingInProgress` |
| `BackingVolumeCreated` | Все Diskful BackingVolume ready | rv-status-conditions-controller | `AllBackingVolumesReady`, `BackingVolumesNotReady`, `WaitingForBackingVolumes` |
| `Configured` | Все RVR Configured | rv-status-conditions-controller | `AllReplicasConfigured`, `ReplicasNotConfigured`, `ConfigurationInProgress` |
| `Initialized` | Достаточно RVR Initialized | rv-status-conditions-controller | `Initialized`, `WaitingForReplicas`, `InitializationInProgress` |
| `Quorum` | Кворум достигнут | rv-status-conditions-controller | `QuorumReached`, `QuorumLost`, `QuorumDegraded` |
| `DataQuorum` | Кворум данных Diskful | rv-status-conditions-controller | `DataQuorumReached`, `DataQuorumLost`, `DataQuorumDegraded` |
| `IOReady` | Достаточно RVR IOReady | rv-status-conditions-controller | `IOReady`, `InsufficientIOReadyReplicas`, `NoIOReadyReplicas` |

### Удаляемые

| Condition | Причина удаления |
|-----------|------------------|
| ~~`Ready`~~ | Неоднозначная семантика "готова к чему?". Заменён на `IOReady`. |
| ~~`AllReplicasReady`~~ | Зависел от удалённого `RVR.Ready`. |
| ~~`QuorumConfigured`~~ | Дублирует проверку `rv.status.drbd.config.quorum != nil`. Потребители могут проверять поле напрямую. |
| ~~`DiskfulReplicaCountReached`~~ | Дублирует информацию из счётчика `diskfulReplicaCount`. Заменён проверкой `current >= desired` из счётчика. |

---

# RVR Conditions (`ReplicatedVolumeReplica.status.conditions[]`)

### `type=Scheduled`

- Обновляется: **rvr-scheduling-controller**.
- `status`:
  - `True` — нода выбрана
    - `rvr.spec.nodeName != ""`
  - `False` — нода не выбрана
- `reason`:
  - `ReplicaScheduled` — реплика успешно назначена на ноду
  - `WaitingForAnotherReplica` — ожидание готовности другой реплики перед планированием
  - `NoAvailableNodes` — нет доступных нод для размещения
  - `TopologyConstraintsFailed` — не удалось выполнить ограничения топологии (Zonal/TransZonal)
  - `InsufficientStorage` — недостаточно места на доступных нодах
- Без изменений относительно текущей реализации.

### `type=BackingVolumeCreated`

- Обновляется: **rvr-volume-controller**.
- `status`:
  - `True` — BackingVolume создан и готов (AND)
    - `rvr.status.lvmLogicalVolumeName != ""`
    - соответствующий LLV (реализация BackingVolume) имеет `status.phase=Created`
  - `False` — BackingVolume не создан или не ready
- `reason`:
  - `BackingVolumeReady` — BackingVolume (LLV) создан и имеет `phase=Created`
  - `BackingVolumeNotReady` — BackingVolume создан, но ещё не ready
  - `WaitingForBackingVolume` — ожидание создания BackingVolume
  - `BackingVolumeCreationFailed` — ошибка создания BackingVolume
  - `NotApplicable` — для `rvr.spec.type != Diskful` (diskless реплики)
- Используется: **rvr-diskful-count-controller** — для определения готовности первой реплики.

### `type=DataInitialized`

- Обновляется: на агенте (предположительно **drbd-config-controller**).
- `status`:
  - `True` — реплика `rvr.spec.type==Diskful` и прошла инициализацию (не снимается!)
    - DRBD ресурс создан и поднят
    - Начальная синхронизация завершена (если требовалась)
  - `False` — инициализация не завершена, либо реплика `rvr.spec.type!=Diskful`
- `reason`:
  - `Initialized` — реплика успешно инициализирована
  - `WaitingForInitialSync` — ожидание завершения начальной синхронизации
  - `InitialSyncInProgress` — начальная синхронизация в процессе
- Примечание: **не снимается** после установки в True — используется для определения "реплика работала".
- Используется: **rvr-diskful-count-controller** — создание следующих реплик только после инициализации первой.

### `type=InQuorum`

- Обновляется: на агенте (предположительно **drbd-status-controller**).
- Ранее: `Quorum`.
- `status`:
  - `True` — реплика в кворуме
    - `rvr.status.drbd.status.devices[0].quorum=true`
  - `False` — реплика вне кворума
  - `Unknown` — нода или agent недоступны
- `reason`:
  - `InQuorum` — реплика участвует в кворуме
  - `QuorumLost` — реплика потеряла кворум (недостаточно подключений)
  - `NodeNotReady` — нода недоступна, статус неизвестен
  - `AgentNotReady` — agent pod не работает, статус неизвестен
- Примечание: `devices[0]` — в текущей версии RVR всегда использует один DRBD volume (индекс 0).
- Примечание: для TieBreaker реплик логика может отличаться.

### `type=InSync`

- Обновляется: на агенте (предположительно **drbd-status-controller**).
- Ранее: `DevicesReady`.
- **Назначение:** Показывает состояние синхронизации данных реплики.
- `status`:
  - `True` — данные синхронизированы
    - Diskful: `rvr.status.drbd.status.devices[0].diskState = UpToDate`
    - Access/TieBreaker: `diskState = Diskless` (всегда True с reason `Diskless`)
  - `False` — данные не синхронизированы
  - `Unknown` — нода или agent недоступны
- `reason`:
  - `InSync` — данные полностью синхронизированы (Diskful, diskState=UpToDate)
  - `Diskless` — diskless реплика (Access/TieBreaker), нет локальных данных, I/O через сеть
  - `Synchronizing` — синхронизация в процессе (diskState=SyncSource/SyncTarget)
  - `OutOfSync` — данные устарели (diskState=Outdated), ожидание resync
  - `Inconsistent` — данные повреждены (diskState=Inconsistent), требуется восстановление
  - `DiskAttaching` — подключение к диску (diskState=Attaching/Negotiating)
  - `NodeNotReady` — нода недоступна, статус неизвестен
  - `AgentNotReady` — agent pod не работает (crash, OOM, evicted), статус неизвестен
- Применимость: все типы реплик.
- **DRBD diskState mapping:**
  - `UpToDate` → reason=`InSync`
  - `SyncSource`, `SyncTarget` → reason=`Synchronizing`
  - `Outdated` → reason=`OutOfSync`
  - `Inconsistent` → reason=`Inconsistent`
  - `Attaching`, `Negotiating`, `DUnknown` → reason=`DiskAttaching`
  - `Diskless` → reason=`Diskless`

### `type=Online`

- Обновляется: **rvr-status-conditions-controller**.
- `status`:
  - `True` — реплика онлайн (AND)
    - `Scheduled=True`
    - `Initialized=True`
    - `InQuorum=True`
  - `False` — реплика не онлайн
- `reason`:
  - `Online` — реплика полностью онлайн
  - `Unscheduled` — реплика не назначена на ноду
  - `Uninitialized` — реплика не прошла инициализацию
  - `QuorumLost` — реплика вне кворума
  - `NodeNotReady` — нода недоступна
  - `AgentNotReady` — agent pod не работает
- Примечание: `Configured` НЕ учитывается — реплика может быть online с устаревшей конфигурацией.

### `type=IOReady`

- Обновляется: **rvr-status-conditions-controller**.
- **Назначение:** Строгая проверка готовности к критическим операциям (resize, promote, snapshot).
- `status`:
  - `True` — реплика **безопасно** готова к I/O (AND)
    - `Online=True`
    - `InSync=True` (diskState=UpToDate)
  - `False` — реплика не готова к безопасным I/O операциям
- `reason`:
  - `IOReady` — реплика полностью готова к I/O операциям
  - `Offline` — реплика не онлайн (смотри `Online` condition)
  - `OutOfSync` — данные не синхронизированы (diskState != UpToDate)
  - `Synchronizing` — идёт синхронизация (SyncSource/SyncTarget)
  - `NodeNotReady` — нода недоступна
  - `AgentNotReady` — agent pod не работает
- Используется: RV.IOReady вычисляется из RVR.IOReady.
- **Примечание:** Гарантирует что данные полностью синхронизированы (diskState=UpToDate).
- **Promote:** Переключение реплики Secondary→Primary. Требует `IOReady=True` чтобы гарантировать актуальность данных и избежать split-brain.


### `type=Configured`

- Обновляется: на агенте (предположительно **drbd-config-controller**).
- Ранее: `ConfigurationAdjusted`.
- `status`:
  - `True` — конфигурация полностью применена (AND)
    - все поля `rvr.status.drbd.actual.*` == соответствующим в `rv.status.drbd.config` или `rvr.status.drbd.config`
    - `rvr.status.drbd.errors.lastAdjustmentError == nil`
    - `rvr.status.drbd.errors.<...>Error == nil`
  - `False` — есть расхождения или ошибки
  - `Unknown` — нода или agent недоступны
- `reason`:
  - `Configured` — конфигурация успешно применена
  - `ConfigurationPending` — ожидание применения конфигурации
  - `ConfigurationFailed` — общая ошибка конфигурации
  - `MetadataCheckFailed` — ошибка проверки DRBD метаданных (`drbdadm dump-md`)
  - `MetadataCreationFailed` — ошибка создания DRBD метаданных (`drbdadm create-md`)
  - `StatusCheckFailed` — не удалось получить статус DRBD (`drbdadm status`)
  - `ResourceUpFailed` — ошибка поднятия ресурса (`drbdadm up`)
  - `AdjustmentFailed` — ошибка применения конфигурации (`drbdadm adjust`)
  - `WaitingForInitialSync` — ожидание начальной синхронизации перед продолжением
  - `PromotionDemotionFailed` — ошибка переключения primary/secondary
  - `NodeNotReady` — нода недоступна, статус неизвестен
  - `AgentNotReady` — agent pod не работает, статус неизвестен
- `message`: детали ошибки из `rvr.status.drbd.errors.*`
- Примечание: может "мигать" при изменении параметров — это нормально.
- Примечание: НЕ включает publish и resize — они отделены.

### `type=Published`

- Обновляется: **rv-publish-controller**.
- Ранее: `Primary`.
- `status`:
  - `True` — реплика опубликована (primary)
    - `rvr.status.drbd.status.role=Primary`
  - `False` — реплика не опубликована
- `reason`:
  - `Published` — реплика является Primary
  - `Unpublished` — реплика является Secondary
  - `PublishPending` — ожидание перехода в Primary
- Применимость: только для `Access` и `Diskful` реплик.
- Примечание: `TieBreaker` не может быть Primary напрямую — требуется сначала изменить тип на `Access`.
- Примечание: НЕ учитывает состояние I/O — только факт публикации.

### `type=AddressConfigured`

- Обновляется: на агенте **rvr-status-config-address-controller**.
- Существующий condition (уже реализован).
- `status`:
  - `True` — адрес DRBD настроен
    - `rvr.status.drbd.config.address.ipv4 != ""`
    - `rvr.status.drbd.config.address.port != 0`
  - `False` — адрес не настроен
- `reason`:
  - `AddressConfigured` — адрес успешно назначен
  - `WaitingForAddress` — ожидание назначения адреса
- Применимость: для всех типов реплик.
- Примечание: контроллер выбирает свободный порт DRBD в диапазоне [1025; 65535].

### Удаляемые conditions

- ~~`type=Ready`~~
  - ❌ Удалить.
  - Причина: непонятная семантика "готова к чему?".
  - Замена: использовать `Online` или `IOReady` в зависимости от контекста.

---

# RV Conditions (`ReplicatedVolume.status.conditions[]`)

### `type=QuorumConfigured`  - убрать

- Обновляется: **rv-status-config-quorum-controller**.
- `status`:
  - `True` — конфигурация кворума применена
    - `rv.status.drbd.config.quorum` установлен
    - `rv.status.drbd.config.quorumMinimumRedundancy` установлен
  - `False` — конфигурация кворума не применена
- `reason`:
  - `QuorumConfigured` — конфигурация кворума успешно применена
  - `WaitingForReplicas` — ожидание готовности реплик для расчёта кворума
- Примечание: показывает что **настройки** кворума применены, а не что кворум **достигнут** (для этого есть `Quorum`).

### `type=DiskfulReplicaCountReached` - удалить - копирует частично `type=IOReady` + counter по diskfull репликам.

- Обновляется: **rvr-diskful-count-controller**.
- `status`:
  - `True` — достигнуто требуемое количество Diskful реплик
    - количество RVR с `spec.type=Diskful` >= требуемое по `rsc.spec.replication`
  - `False` — недостаточно Diskful реплик
- `reason`:
  - `RequiredNumberOfReplicasIsAvailable` — все требуемые реплики созданы
  - `FirstReplicaIsBeingCreated` — создаётся первая реплика
  - `WaitingForFirstReplica` — ожидание готовности первой реплики
- Примечание: контролирует создание Diskful реплик, первая реплика должна быть Initialized перед созданием остальных.

### `type=IOReady`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — достаточно реплик готовы к I/O
    - достаточное количество RVR (согласно QMR + RSC) имеют `IOReady=True`
    - QMR = quorumMinimumRedundancy (минимум Diskful реплик для кворума данных)
    - RSC = ReplicatedStorageClass (определяет требования репликации)
  - `False` — недостаточно готовых реплик
- `reason`:
  - `IOReady` — volume готов к I/O операциям
  - `InsufficientIOReadyReplicas` — недостаточно IOReady реплик
  - `NoIOReadyReplicas` — нет ни одной IOReady реплики
- TODO: уточнить точную формулу threshold для IOReady (предположительно >= 1 реплика).
- Используется: **rv-publish-controller**, **drbd-resize-controller**, **drbd-primary-controller**.

### `type=Scheduled`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — все реплики назначены на ноды
    - все RVR имеют `Scheduled=True`
  - `False` — есть неназначенные реплики
- `reason`:
  - `AllReplicasScheduled` — все реплики назначены
  - `ReplicasNotScheduled` — есть реплики без назначенной ноды
  - `SchedulingInProgress` — планирование в процессе

### `type=BackingVolumeCreated`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — все BackingVolume созданы и готовы
    - все Diskful RVR имеют `BackingVolumeCreated=True`
  - `False` — есть неготовые BackingVolume
- `reason`:
  - `AllBackingVolumesReady` — все BackingVolume готовы
  - `BackingVolumesNotReady` — есть неготовые BackingVolume
  - `WaitingForBackingVolumes` — ожидание создания BackingVolume

### `type=Configured`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — все реплики сконфигурированы
    - все RVR имеют `Configured=True`
  - `False` — есть несконфигурированные реплики или Unknown
- `reason`:
  - `AllReplicasConfigured` — все реплики сконфигурированы
  - `ReplicasNotConfigured` — есть несконфигурированные реплики
  - `ConfigurationInProgress` — конфигурация в процессе

### `type=Initialized`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — достаточно реплик инициализировано (один раз, далее НЕ снимается)
    - достаточное количество RVR (согласно `rsc.spec.replication`) имеют `Initialized=True`
  - `False` — до достижения порога
- `reason`:
  - `Initialized` — достаточное количество реплик инициализировано
  - `WaitingForReplicas` — ожидание инициализации реплик
  - `InitializationInProgress` — инициализация в процессе
- Порог "достаточного количества":
  - `None`: 1 реплика
  - `Availability`: 2 реплики
  - `ConsistencyAndAvailability`: 3 реплики

### `type=Quorum`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — есть кворум
    - количество RVR с `InQuorum=True` >= `rv.status.drbd.config.quorum`
  - `False` — кворума нет
- `reason`:
  - `QuorumReached` — кворум достигнут
  - `QuorumLost` — кворум потерян
  - `QuorumDegraded` — кворум на грани (N+0)
- Формула расчёта `quorum`:
  ```
  N = все реплики (Diskful + TieBreaker + Access)
  M = только Diskful реплики
  
  if M > 1:
    quorum = max(2, N/2 + 1)
  else:
    quorum = 0  // кворум отключён для single-replica
  ```
- Примечание: использует `InQuorum`, а не `InSync` — проверяет **подключение**, а не **синхронизацию**.

### `type=DataQuorum`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — есть кворум данных (только Diskful реплики)
    - количество Diskful RVR с `InQuorum=True` >= `rv.status.drbd.config.quorumMinimumRedundancy`
  - `False` — кворума данных нет
- `reason`:
  - `DataQuorumReached` — кворум данных достигнут
  - `DataQuorumLost` — кворум данных потерян
  - `DataQuorumDegraded` — кворум данных на грани
- Формула расчёта `quorumMinimumRedundancy` (QMR):
  ```
  M = только Diskful реплики
  
  if M > 1:
    qmr = max(2, M/2 + 1)
  else:
    qmr = 0  // QMR отключён для single-replica
  ```
- Примечание: учитывает только Diskful реплики — **носители данных**.
- Примечание: использует `InQuorum` (подключение), а не `InSync` (синхронизация).
- Связь с другими полями:
  - `Quorum` — кворум по всем репликам (защита от split-brain)
  - `DataQuorum` — кворум среди носителей данных (защита данных от split-brain)
  - `diskfulReplicasInSync` counter — сколько реплик имеют **актуальные** данные

---

## `status` (counters — не conditions)

- `diskfulReplicaCount`
  - Тип: string.
  - Формат: `current/desired` (например, `3/3`).
  - Обновляется: **rv-status-conditions-controller**.
  - Описание: количество Diskful реплик / желаемое количество.

- `diskfulReplicasInSync`
  - Тип: string.
  - Формат: `current/total` (например, `2/3`).
  - Обновляется: **rv-status-conditions-controller**.
  - Описание: количество синхронизированных Diskful реплик / всего Diskful реплик.

- `publishedAndIOReadyCount`
  - Тип: string.
  - Формат: `current/requested` (например, `1/1`).
  - Обновляется: **rv-status-conditions-controller**.
  - Описание: количество опубликованных и IOReady реплик / запрошено для публикации.

---

# Future Conditions in wave3 (следующий этап)

## RV Future Conditions

### `type=QuorumAtRisk`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — кворум есть, но на грани (AND)
    - `Quorum=True`
    - количество RVR с `InQuorum=True` == `rv.status.drbd.config.quorum` (ровно на границе)
  - `False` — кворум с запасом или кворума нет
- `reason`:
  - `QuorumAtRisk` — кворум на грани, нет запаса (N+0)
  - `QuorumSafe` — кворум с запасом (N+1 или больше)
  - `QuorumLost` — кворума нет
- Описание: кворум есть, но нет N+1. Потеря одной реплики приведёт к потере кворума.
- Применение: alerting, UI warning.

### `type=DataQuorumAtRisk`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — кворум данных под угрозой (OR)
    - `DataQuorum=True` AND количество Diskful RVR с `InQuorum=True` == QMR (ровно на границе)
    - `DataQuorum=True` AND НЕ все Diskful RVR имеют `InSync=True`
  - `False` — кворум данных безопасен
- `reason`:
  - `DataQuorumAtRisk` — кворум данных на грани
  - `DataQuorumSafe` — кворум данных с запасом
  - `DataQuorumLost` — кворум данных потерян
  - `ReplicasOutOfSync` — есть несинхронизированные реплики
- Описание: кворум данных есть, но нет N+1, или не все InSync.
- Применение: alerting, UI warning.

### `type=DataAtRisk`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — данные в единственном экземпляре
    - количество Diskful RVR с `InSync=True` == 1
  - `False` — данные реплицированы
- `reason`:
  - `DataAtRisk` — данные только на одной реплике
  - `DataRedundant` — данные реплицированы на несколько реплик
- Описание: данные в единственном экземпляре. Потеря этой реплики = потеря данных.
- Применение: critical alerting, UI critical warning.

### `type=SplitBrain`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — обнаружен split-brain
  - `False` — split-brain не обнаружен
- `reason`:
  - `SplitBrainDetected` — обнаружен split-brain
  - `NoSplitBrain` — split-brain не обнаружен
  - `SplitBrainResolved` — split-brain был, но разрешён
- Описание: требуется исследование логики определения.
- Возможные признаки:
  - несколько Primary реплик без `allowTwoPrimaries`
  - `rvr.status.drbd.status.connections[].connectionState=SplitBrain`
  - несовпадение данных между репликами (out-of-sync с обеих сторон)
- TODO: требуется детальное исследование DRBD status для определения.

## RVR Future Conditions

### `type=FullyConnected`

- Обновляется: на агенте (предположительно **drbd-status-controller**).
- `status`:
  - `True` — есть связь со всеми peers
    - `len(rvr.status.drbd.status.connections) == len(rvr.status.drbd.config.peers)`
    - все connections имеют `connectionState=Connected`
  - `False` — нет связи с частью peers
- `reason`:
  - `FullyConnected` — связь со всеми peers установлена
  - `PartiallyConnected` — связь только с частью peers
  - `Disconnected` — нет связи ни с одним peer
  - `Connecting` — установка соединений в процессе
- Примечание: НЕ влияет на `Online` или `IOReady`.
- Применение: диагностика сетевых проблем.

### `type=ResizeInProgress`

- Обновляется: на агенте (предположительно **drbd-resize-controller**).
- `status`:
  - `True` — resize операция в процессе
    - `rv.spec.size > rv.status.actualSize`
  - `False` — resize не требуется или завершён
- `reason`:
  - `ResizeInProgress` — изменение размера в процессе
  - `ResizeCompleted` — изменение размера завершено
  - `ResizeNotNeeded` — изменение размера не требуется
  - `ResizeFailed` — ошибка изменения размера
- Применение: UI индикация, блокировка некоторых операций.

---

# Спецификации контроллеров conditions

## rvr-status-conditions-controller

### Цель

Вычислять computed RVR conditions с проверкой доступности ноды/агента.

### Архитектура

```go
builder.ControllerManagedBy(mgr).
    For(&v1alpha3.ReplicatedVolumeReplica{}).
    Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(agentPodToRVRMapper),
        builder.WithPredicates(agentPodPredicate)).
    Complete(rec)
```

### Условия

| Condition | Логика | Примерный список reasons |
|-----------|--------|--------------------------|
| `Online` | `Scheduled ∧ Initialized ∧ InQuorum` → True | `Online`, `Unscheduled`, `Uninitialized`, `QuorumLost`, `NodeNotReady`, `AgentNotReady` |
| `IOReady` | `Online ∧ InSync` → True | `IOReady`, `Offline`, `OutOfSync`, `Synchronizing`, `NodeNotReady`, `AgentNotReady` |

> **Примерный список reasons, добавьте/уберите если необходимо.**

### Проверка доступности

```
1. Get Agent Pod:
   - labels: app=sds-drbd-agent, spec.nodeName=rvr.spec.nodeName
   - If Pod not found OR phase != Running OR Ready != True:
     → Agent NotReady, продолжаем к шагу 2

2. If Agent NotReady — определяем reason:
   - Get Node by rvr.spec.nodeName
   - If node not found OR node.Ready == False/Unknown:
     → reason = NodeNotReady
   - Else:
     → reason = AgentNotReady

3. Set conditions:
   RVR.Online  = False, reason = <NodeNotReady|AgentNotReady>
   RVR.IOReady = False, reason = <NodeNotReady|AgentNotReady>
```

### Сценарии

**NodeNotReady:**
- Node failure (нода упала)
- Node unreachable (network partition)
- Kubelet не отвечает (node.Ready = Unknown)

**AgentNotReady (node OK):**
- Agent pod CrashLoopBackOff
- Agent pod OOMKilled
- Agent pod Evicted
- Agent pod Pending/Terminating

### Вывод

- `rvr.status.conditions[type=Online]`
- `rvr.status.conditions[type=IOReady]`

---

## rv-status-conditions-controller

### Цель

Агрегировать RVR conditions в RV conditions и обновлять счётчики.

### Архитектура

```go
builder.ControllerManagedBy(mgr).
    For(&v1alpha3.ReplicatedVolume{}).
    Owns(&v1alpha3.ReplicatedVolumeReplica{}).
    Complete(rec)
```

### Условия

| Condition | Логика | Примерный список reasons |
|-----------|--------|--------------------------|
| `Scheduled` | ALL `RVR.Scheduled=True` | `AllReplicasScheduled`, `ReplicasNotScheduled`, `SchedulingInProgress` |
| `BackingVolumeCreated` | ALL Diskful `RVR.BackingVolumeCreated=True` | `AllBackingVolumesReady`, `BackingVolumesNotReady`, `WaitingForBackingVolumes` |
| `Configured` | ALL `RVR.Configured=True` | `AllReplicasConfigured`, `ReplicasNotConfigured`, `ConfigurationInProgress` |
| `Initialized` | count(Initialized=True) >= threshold | `Initialized`, `WaitingForReplicas`, `InitializationInProgress` |
| `Quorum` | count(All InQuorum=True) >= quorum | `QuorumReached`, `QuorumLost`, `QuorumDegraded` |
| `DataQuorum` | count(Diskful InSync=True) >= QMR | `DataQuorumReached`, `DataQuorumLost`, `DataQuorumDegraded` |
| `IOReady` | count(Diskful IOReady=True) >= threshold | `IOReady`, `InsufficientIOReadyReplicas`, `NoIOReadyReplicas` |

> **Примерный список reasons, добавьте/уберите если необходимо.**

### Счётчики

| Counter | Формат | Описание |
|---------|--------|----------|
| `diskfulReplicaCount` | `current/desired` | Diskful реплик |
| `diskfulReplicasInSync` | `current/total` | InSync Diskful реплик |
| `publishedAndIOReadyCount` | `current/requested` | Published + IOReady |

### Вывод

- `rv.status.conditions[type=*]`
- `rv.status.diskfulReplicaCount`
- `rv.status.diskfulReplicasInSync`
- `rv.status.publishedAndIOReadyCount`

---

## Время обнаружения

| Метод | Контроллер | Что обнаруживает | Скорость |
|-------|------------|------------------|----------|
| Agent Pod watch | rvr-status-conditions-controller | Agent crash/OOM/evict | ~секунды |
| Agent Pod watch | rvr-status-conditions-controller | Node failure (pod → Unknown/Failed) | ~секунды |
| Owns(RVR) | rv-status-conditions-controller | RVR condition changes, quorum loss | ~секунды |

**Как это работает:**

1. **rvr-status-conditions-controller** — смотрит на Agent Pod, если pod недоступен — проверяет Node.Ready и ставит `NodeNotReady` или `AgentNotReady`.

2. **rv-status-conditions-controller** — получает события через `Owns(RVR)` когда RVR условия меняются (включая изменения от DRBD агентов на других нодах).

**Примечание о DRBD:**
Если нода падает, DRBD агент на других нодах обнаружит потерю connection и обновит свой `rvr.status.drbd.status.connections[]`. Это триггерит reconcile для `rv-status-conditions-controller` через `Owns(RVR)`.


---

# Влияние на контроллеры (удаление conditions)

### rvr-diskful-count-controller

| Изменение | Описание |
|-----------|----------|
| Read: `Ready` → `Initialized` | Проверяем `Initialized=True` вместо `Ready=True` |
| ❌ Убрать: `DiskfulReplicaCountReached` | Дублирует счётчик `diskfulReplicaCount` |

### rv-status-config-quorum-controller

| Изменение | Описание |
|-----------|----------|
| ❌ Убрать: `QuorumConfigured` | Дублирует `quorum != nil` |
| ❌ Убрать: `AllReplicasReady` | Зависит от удалённого `Ready` |
| ❌ Убрать: `DiskfulReplicaCountReached` | Использовать счётчик `diskfulReplicaCount` |
| 🆕 Read: `RV.Configured` | Заменяет все проверки sharedSecret |

**Новая логика `isReadyForQuorum`(пример):**
```go
current, desired := parseDiskfulReplicaCount(rv.status.diskfulReplicaCount)
return current >= desired && current > 0 && RV.Configured=True
```

**Потребители `QuorumConfigured`:** проверять `rv.status.drbd.config.quorum != nil`.

---

