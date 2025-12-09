# Спецификация изменений Conditions (v1alpha3)

## Обзор: RVR Conditions

### Phase 1 — необходимо для работы системы

| Condition | Статус | Описание | Контроллер | Reasons |
|-----------|--------|----------|------------|---------|
| `Scheduled` | существует | Нода выбрана | rvr-scheduling-controller | `ReplicaScheduled`, `WaitingForAnotherReplica`, `NoAvailableNodes`, ... |
| `BackingVolumeCreated` | 🆕 новый | LLV создан и ready | rvr-volume-controller | `BackingVolumeReady`, `BackingVolumeNotReady`, `WaitingForLLV`, ... |
| `Initialized` | 🆕 новый | Инициализация (не снимается) | drbd-config-controller | `Initialized`, `WaitingForInitialSync`, `InitialSyncInProgress` |
| `InQuorum` | переименован | Реплика в кворуме | rvr-status-conditions-controller | `InQuorum`, `QuorumLost` |
| `InSync` | переименован | Данные синхронизированы | rvr-status-conditions-controller | `InSync`, `Synchronizing`, `OutOfSync`, `Inconsistent`, `Diskless` |
| `Online` | 🆕 computed | Scheduled + Initialized + InQuorum | rvr-status-conditions-controller | `Online`, `Unscheduled`, `Uninitialized`, `QuorumLost` |
| `IOReady` | 🆕 computed | Online + InSync | rvr-status-conditions-controller | `IOReady`, `Offline`, `OutOfSync` |

### Phase 2 — расширение функциональности

| Condition | Статус | Описание | Контроллер | Reasons |
|-----------|--------|----------|------------|---------|
| `Configured` | переименован | Конфигурация применена | rvr-status-conditions-controller | `Configured`, `ConfigurationFailed`, `AdjustmentFailed`, ... |
| `Published` | переименован | Реплика Primary | rv-publish-controller | `Published`, `Unpublished`, `PublishPending` |

### Удаляемые

| Condition | Причина |
|-----------|---------|
| ~~`Ready`~~ | Непонятная семантика |

---

## Обзор: RV Conditions

### Phase 1 — необходимо для работы системы

| Condition | Статус | Описание | Контроллер | Reasons |
|-----------|--------|----------|------------|---------|
| `QuorumConfigured` | существует | Конфигурация кворума | rv-status-config-quorum-controller | `QuorumConfigured`, `WaitingForReplicas` |
| `DiskfulReplicaCountReached` | существует | Кол-во Diskful достигнуто | rvr-diskful-count-controller | `RequiredNumberOfReplicasIsAvailable`, `FirstReplicaIsBeingCreated` |
| `SharedSecretAlgorithmSelected` | существует | Алгоритм shared secret | rv-status-config-shared-secret-controller | `AlgorithmSelected`, `UnableToSelectSharedSecretAlgorithm` |
| `IOReady` | 🆕 новый | Достаточно RVR IOReady | rv-status-conditions-controller | `IOReady`, `InsufficientIOReadyReplicas`, `NoIOReadyReplicas` |

### Phase 2 — расширение функциональности

| Condition | Статус | Описание | Контроллер | Reasons |
|-----------|--------|----------|------------|---------|
| `Scheduled` | 🆕 новый | Все RVR Scheduled | rv-status-conditions-controller | `AllReplicasScheduled`, `ReplicasNotScheduled` |
| `BackingVolumeCreated` | 🆕 новый | Все Diskful LLV ready | rv-status-conditions-controller | `AllBackingVolumesReady`, `BackingVolumesNotReady` |
| `Configured` | 🆕 новый | Все RVR Configured | rv-status-conditions-controller | `AllReplicasConfigured`, `ReplicasNotConfigured` |
| `Initialized` | 🆕 новый | Достаточно RVR Initialized | rv-status-conditions-controller | `Initialized`, `WaitingForReplicas` |
| `Quorum` | 🆕 новый | Кворум достигнут | rv-status-conditions-controller | `QuorumReached`, `QuorumLost` |
| `DataQuorum` | 🆕 новый | Кворум данных Diskful | rv-status-conditions-controller | `DataQuorumReached`, `DataQuorumLost` |

### Удаляемые

| Condition | Причина |
|-----------|---------|
| ~~`Ready`~~ | Непонятная семантика |
| ~~`AllReplicasReady`~~ | Зависел от Ready |

---

# RVR Conditions (`ReplicatedVolumeReplica.status.conditions[]`)

## Phase 1 — необходимо для работы системы

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
  - `True` — LLV создан и готов (AND)
    - `rvr.status.lvmLogicalVolumeName != ""`
    - соответствующий LLV имеет `status.phase=Ready`
  - `False` — LLV не создан или не ready
  - `Unknown` — не применимо для данного типа реплики
- `reason`:
  - `BackingVolumeReady` — LLV создан и имеет `phase=Ready`
  - `BackingVolumeNotReady` — LLV создан, но ещё не ready
  - `WaitingForLLV` — ожидание создания LLV
  - `LLVCreationFailed` — ошибка создания LLV
  - `NotApplicable` — для `rvr.spec.type != Diskful` (diskless реплики)
- Используется: **rvr-diskful-count-controller** — для определения готовности первой реплики.

### `type=Initialized`

- Обновляется: **drbd-config-controller** (agent).
- 🆕 Новый condition.
- `status`:
  - `True` — реплика прошла инициализацию (не снимается!)
    - DRBD ресурс создан и поднят
    - Начальная синхронизация завершена (если требовалась)
  - `False` — инициализация не завершена
- `reason`:
  - `Initialized` — реплика успешно инициализирована
  - `WaitingForInitialSync` — ожидание завершения начальной синхронизации
  - `InitialSyncInProgress` — начальная синхронизация в процессе
- Примечание: **не снимается** после установки в True — используется для определения "реплика работала".
- Используется: **rvr-diskful-count-controller** — создание следующих реплик только после инициализации первой.

### `type=InQuorum`

- Обновляется: **rvr-status-conditions-controller**.
- Ранее: `Quorum`.
- `status`:
  - `True` — реплика в кворуме
    - `rvr.status.drbd.status.connection.quorum=true`
  - `False` — реплика вне кворума
- `reason`:
  - `InQuorum` — реплика участвует в кворуме
  - `QuorumLost` — реплика потеряла кворум (недостаточно подключений)
- Примечание: для TieBreaker реплик логика может отличаться.

### `type=InSync`

- Обновляется: **rvr-status-conditions-controller**.
- Ранее: `DevicesReady`.
- `status`:
  - `True` — данные синхронизированы
    - `rvr.status.drbd.status.connection.diskState = UpToDate`
  - `False` — данные не синхронизированы
- `reason`:
  - `InSync` — данные полностью синхронизированы
  - `Synchronizing` — синхронизация в процессе (есть progress %)
  - `OutOfSync` — данные рассинхронизированы, синхронизация не идёт
  - `Inconsistent` — данные в несогласованном состоянии
  - `Diskless` — реплика без диска (Access type)
- Применимость: для Diskful и TieBreaker реплик.

### `type=Online`

- Обновляется: **rvr-status-conditions-controller**.
- 🆕 Вычисляемый (computed).
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
- Примечание: `Configured` НЕ учитывается — реплика может быть online с устаревшей конфигурацией.

### `type=IOReady`

- Обновляется: **rvr-status-conditions-controller**.
- 🆕 Вычисляемый (computed).
- `status`:
  - `True` — реплика готова к I/O (AND)
    - `Online=True`
    - `InSync=True`
  - `False` — реплика не готова к I/O
- `reason`:
  - `IOReady` — реплика полностью готова к I/O операциям
  - `Offline` — реплика не онлайн (смотри `Online` condition)
  - `OutOfSync` — данные не синхронизированы (смотри `InSync` condition)
- Используется: RV.IOReady вычисляется из RVR.IOReady.

---

## Phase 2 — расширение функциональности

### `type=Configured`

- Обновляется: **rvr-status-conditions-controller** / **drbd-config-controller** (agent).
- Ранее: `ConfigurationAdjusted`.
- `status`:
  - `True` — конфигурация полностью применена (AND)
    - все поля `rvr.status.drbd.actual.*` == соответствующим в `rv.status.drbd.config` или `rvr.status.drbd.config`
    - `rvr.status.drbd.errors.lastAdjustmentError == nil`
    - `rvr.status.drbd.errors.<...>Error == nil`
  - `False` — есть расхождения или ошибки
- `reason`:
  - `Configured` — конфигурация успешно применена
  - `ConfigurationFailed` — общая ошибка конфигурации
  - `MetadataCheckFailed` — ошибка проверки DRBD метаданных (`drbdadm dump-md`)
  - `MetadataCreationFailed` — ошибка создания DRBD метаданных (`drbdadm create-md`)
  - `StatusCheckFailed` — не удалось получить статус DRBD (`drbdadm status`)
  - `ResourceUpFailed` — ошибка поднятия ресурса (`drbdadm up`)
  - `AdjustmentFailed` — ошибка применения конфигурации (`drbdadm adjust`)
  - `WaitingForInitialSync` — ожидание начальной синхронизации перед продолжением
  - `PromotionDemotionFailed` — ошибка переключения primary/secondary
- `message`: детали ошибки из `rvr.status.drbd.errors.*`
- Примечание: может "мигать" при изменении параметров — это нормально.
- Примечание: НЕ включает publish и resize — они отделены.

### `type=Published`

- Обновляется: **rv-publish-controller**.
- Ранее: `VolumeAccessReady` (с другой логикой).
- `status`:
  - `True` — реплика опубликована (primary)
    - `rvr.status.drbd.status.role=Primary`
  - `False` — реплика не опубликована
- `reason`:
  - `Published` — реплика является Primary
  - `Unpublished` — реплика является Secondary
  - `PublishPending` — ожидание перехода в Primary
- Применимость: только для `Access` и `Diskful` реплик.
- Примечание: НЕ учитывает состояние I/O — только факт публикации.

### Удаляемые conditions

- ~~`type=Ready`~~
  - ❌ Удалить.
  - Причина: непонятная семантика "готова к чему?".
  - Замена: использовать `Online` или `IOReady` в зависимости от контекста.

---

# RV Conditions (`ReplicatedVolume.status.conditions[]`)

## Phase 1 — необходимо для работы системы

### `type=QuorumConfigured`

- Обновляется: **rv-status-config-quorum-controller**.
- Существующий condition (без изменений).
- `status`:
  - `True` — конфигурация кворума применена
    - `rv.status.drbd.config.quorum` установлен
    - `rv.status.drbd.config.quorumMinimumRedundancy` установлен
  - `False` — конфигурация кворума не применена
- `reason`:
  - `QuorumConfigured` — конфигурация кворума успешно применена
  - `WaitingForReplicas` — ожидание готовности реплик для расчёта кворума
- Примечание: показывает что **настройки** кворума применены, а не что кворум **достигнут** (для этого есть `Quorum`).

### `type=DiskfulReplicaCountReached`

- Обновляется: **rvr-diskful-count-controller**.
- Существующий condition (без изменений).
- `status`:
  - `True` — достигнуто требуемое количество Diskful реплик
    - количество RVR с `spec.type=Diskful` >= требуемое по `rsc.spec.replication`
  - `False` — недостаточно Diskful реплик
- `reason`:
  - `RequiredNumberOfReplicasIsAvailable` — все требуемые реплики созданы
  - `FirstReplicaIsBeingCreated` — создаётся первая реплика
  - `WaitingForFirstReplica` — ожидание готовности первой реплики
- Примечание: контролирует создание Diskful реплик, первая реплика должна быть ready перед созданием остальных.

### `type=SharedSecretAlgorithmSelected`

- Обновляется: **rv-status-config-shared-secret-controller**.
- Существующий condition (без изменений).
- `status`:
  - `True` — алгоритм shared secret выбран и работает
    - `rv.status.drbd.config.sharedSecretAlg` установлен
    - нет ошибок на репликах
  - `False` — не удалось выбрать рабочий алгоритм
- `reason`:
  - `AlgorithmSelected` — алгоритм успешно выбран
  - `UnableToSelectSharedSecretAlgorithm` — все алгоритмы исчерпаны, ни один не работает
- Алгоритмы (в порядке приоритета): `sha256`, `sha1`.

### `type=IOReady`

- Обновляется: **rv-status-conditions-controller**.
- 🆕 Новый condition.
- `status`:
  - `True` — достаточно реплик готовы к I/O
    - достаточное количество RVR (согласно QMR + RSC) имеют `IOReady=True`
  - `False` — недостаточно готовых реплик
- `reason`:
  - `IOReady` — volume готов к I/O операциям
  - `InsufficientIOReadyReplicas` — недостаточно IOReady реплик
  - `NoIOReadyReplicas` — нет ни одной IOReady реплики
- Используется: **rv-publish-controller**, **drbd-resize-controller**, **drbd-primary-controller**.

---

## Phase 2 — расширение функциональности

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
  - `True` — все LLV созданы и готовы
    - все Diskful RVR имеют `BackingVolumeCreated=True`
  - `False` — есть неготовые LLV
- `reason`:
  - `AllBackingVolumesReady` — все LLV готовы
  - `BackingVolumesNotReady` — есть неготовые LLV
  - `WaitingForBackingVolumes` — ожидание создания LLV

### `type=Configured`

- Обновляется: **rv-status-conditions-controller**.
- `status`:
  - `True` — все реплики сконфигурированы
    - все RVR имеют `Configured=True`
  - `False` — есть несконфигурированные реплики
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

# Future Conditions (следующий этап)

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

- Обновляется: **rv-status-conditions-controller** или **rvr-status-conditions-controller**.
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

- Обновляется: **rvr-status-conditions-controller**.
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

- Обновляется: **drbd-resize-controller** (agent).
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

# Summary: Conditions по контроллерам

## RVR Controllers

### rvr-scheduling-controller
| Condition | Действие |
|-----------|----------|
| `Scheduled` | set |

### rvr-volume-controller
| Condition | Действие |
|-----------|----------|
| `BackingVolumeCreated` | set |

### drbd-config-controller (agent)
| Condition | Действие |
|-----------|----------|
| `Initialized` | set |
| `Configured` | set (частично) |

### rv-publish-controller
| Condition | Действие |
|-----------|----------|
| `Published` | set |

### rvr-status-conditions-controller
| Condition | Действие |
|-----------|----------|
| `Configured` | set/compute |
| `InQuorum` | set |
| `InSync` | set |
| `Online` | compute |
| `IOReady` | compute |
| `FullyConnected` | set (future) |

## RV Controllers

### rv-status-conditions-controller
| Condition | Действие | Источник |
|-----------|----------|----------|
| `Scheduled` | aggregate | from RVR.Scheduled |
| `BackingVolumeCreated` | aggregate | from RVR.BackingVolumeCreated |
| `Configured` | aggregate | from RVR.Configured |
| `Initialized` | aggregate | from RVR.Initialized |
| `Quorum` | compute | RVR.InQuorum + config |
| `DataQuorum` | compute | Diskful RVR.InQuorum + QMR |
| `IOReady` | compute | RVR.IOReady + thresholds |
| `QuorumAtRisk` | compute (future) | Quorum margin |
| `DataQuorumAtRisk` | compute (future) | DataQuorum margin |
| `DataAtRisk` | compute (future) | InSync count |
| `SplitBrain` | compute (future) | DRBD status |

---

# Влияние на контроллеры

## Требуется изменить

- **rvr-diskful-count-controller**
  - Было: проверяет `rvr.status.conditions[type=Ready].status=True`
  - Стало: проверяет `rvr.status.conditions[type=Initialized].status=True`
  - Альтернатива: `BackingVolumeCreated=True` для первой реплики

- **rvr-gc-controller**
  - Было: проверяет `Ready=True && FullyConnected=True`
  - Стало: проверяет `Online=True` или `IOReady=True`

- **rv-publish-controller**
  - Было: проверяет `rv.status.conditions[type=Ready].status=True`
  - Стало: проверяет `rv.status.conditions[type=IOReady].status=True`

- **drbd-resize-controller** (agent)
  - Было: проверяет `rv.status.conditions[type=Ready].status=True`
  - Стало: проверяет `rv.status.conditions[type=IOReady].status=True`

- **drbd-primary-controller** (agent)
  - Было: проверяет `rv.status.conditions[type=Ready].status=True`
  - Стало: проверяет `rv.status.conditions[type=IOReady].status=True`

---

