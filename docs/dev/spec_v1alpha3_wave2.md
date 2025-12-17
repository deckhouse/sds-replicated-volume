- [Акторы приложения: `agent`](#акторы-приложения-agent)
  - [`drbd-config-controller`](#drbd-config-controller)
  - [`drbd-resize-controller`](#drbd-resize-controller)
    - [Статус: \[OK | priority: 5 | complexity: 2\]](#статус-ok--priority-5--complexity-2)
  - [`drbd-primary-controller`](#drbd-primary-controller)
  - [`rvr-status-config-address-controller`](#rvr-status-config-address-controller)
- [Акторы приложения: `controller`](#акторы-приложения-controller)
  - [`rvr-diskful-count-controller`](#rvr-diskful-count-controller)
  - [`rvr-scheduling-controller`](#rvr-scheduling-controller)
  - [`rvr-status-config-node-id-controller`](#rvr-status-config-node-id-controller)
  - [`rvr-status-config-peers-controller`](#rvr-status-config-peers-controller)
  - [`rv-status-config-device-minor-controller`](#rv-status-config-device-minor-controller)
  - [`rvr-tie-breaker-count-controller`](#rvr-tie-breaker-count-controller)
  - [`rvr-access-count-controller`](#rvr-access-count-controller)
  - [`rv-publish-controller`](#rv-publish-controller)
  - [`rvr-volume-controller`](#rvr-volume-controller)
  - [`rvr-quorum-and-publish-constrained-release-controller`](#rvr-quorum-and-publish-constrained-release-controller)
  - [`rvr-owner-reference-controller`](#rvr-owner-reference-controller)
  - [`rv-status-config-quorum-controller`](#rv-status-config-quorum-controller)
  - [`rv-status-config-shared-secret-controller`](#rv-status-config-shared-secret-controller)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller)
  - [`rvr-node-cordon-controller`](#rvr-node-cordon-controller)
  - [`rvr-status-conditions-controller`](#rvr-status-conditions-controller)
  - [`llv-owner-reference-controller`](#llv-owner-reference-controller)
  - [`rv-status-conditions-controller`](#rv-status-conditions-controller)
  - [`rv-gc-controller`](#rv-gc-controller)
  - [`tie-breaker-removal-controller`](#tie-breaker-removal-controller)
  - [`rvr-finalizer-release-controller`](#rvr-finalizer-release-controller)
    - [Статус: \[OK | priority: 5 | complexity: 3\]](#статус-ok--priority-5--complexity-3)
  - [`rv-finalizer-controller`](#rv-finalizer-controller)
    - [Статус: \[OK | priority: 5 | complexity: 1\]](#статус-ok--priority-5--complexity-1)
  - [`rv-delete-propagation-controller`](#rv-delete-propagation-controller)
    - [Статус: \[OK | priority: 5 | complexity: 1\]](#статус-ok--priority-5--complexity-1-1)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller-1)
    - [Статус: \[TBD | priority: 3 | complexity: 3\]](#статус-tbd--priority-3--complexity-3)
  - [`llv-owner-reference-controller`](#llv-owner-reference-controller-1)
    - [Статус: \[TBD | priority: 5 | complexity: 1\]](#статус-tbd--priority-5--complexity-1)
  - [`rv-status-conditions-controller`](#rv-status-conditions-controller-1)
  - [`rv-gc-controller`](#rv-gc-controller-1)
  - [`tie-breaker-removal-controller`](#tie-breaker-removal-controller-1)

# Акторы приложения: `agent`

## `drbd-config-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

Если на rvr/rv есть `metadata.deletionTimestamp` и не наш финализатор (не `sds-replicated-volume.storage.deckhouse.io/*`),
то объект не должен считаться удалённым. Любая логика, связанная с обработкой удалённых rv/rvr должна
быть обновлена, чтобы включать это условие.

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

Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

### Вывод 
 - `rvr.status.drbd.errors.*`
 - `rv.status.actualSize`

## `drbd-primary-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

Если на rvr/rv есть `metadata.deletionTimestamp` и не наш финализатор (не `sds-replicated-volume.storage.deckhouse.io/*`),
то объект не должен считаться удалённым. Любая логика, связанная с обработкой удалённых rv/rvr должна
быть обновлена, чтобы включать это условие.

## `rvr-status-config-address-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

Если на rvr/rv есть `metadata.deletionTimestamp` и не наш финализатор (не `sds-replicated-volume.storage.deckhouse.io/*`),
то объект не должен считаться удалённым. Любая логика, связанная с обработкой удалённых rv/rvr должна
быть обновлена, чтобы включать это условие.

# Акторы приложения: `controller`

## `rvr-diskful-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

В случае, если в rv стоит `metadata.deletionTimestamp` и только наши финализаторы
`sds-replicated-volume.storage.deckhouse.io/*` (нет чужих), новые реплики не создаются.

## `rvr-scheduling-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-config-node-id-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-config-peers-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-device-minor-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-tie-breaker-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

В случае, если в rv стоит `metadata.deletionTimestamp` и только наши финализаторы
`sds-replicated-volume.storage.deckhouse.io/*` (нет чужих), новые реплики не создаются.

## `rvr-access-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

В случае, если в rv стоит `metadata.deletionTimestamp` и только наши финализаторы
`sds-replicated-volume.storage.deckhouse.io/*` (нет чужих), новые реплики не создаются.

## `rv-publish-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

В случае, если в rv стоит `metadata.deletionTimestamp` и только наши финализаторы
`sds-replicated-volume.storage.deckhouse.io/*` (нет чужих) - убираем публикацию со всех rvr данного rv и
не публикуем новые rvr для данного rv.

## `rvr-volume-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-quorum-and-publish-constrained-release-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-owner-reference-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-quorum-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-shared-secret-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-missing-node-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-node-cordon-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-conditions-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `llv-owner-reference-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-conditions-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-gc-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `tie-breaker-removal-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-finalizer-release-controller`

### Статус: [OK | priority: 5 | complexity: 3]

### Обновление

Контроллер заменяет `rvr-quorum-and-publish-constrained-release-controller`

### Контекст

Приложение agent ставит 2 финализатора на все RVR до того, как сконфигурирует DRBD.
  - `sds-replicated-volume.storage.deckhouse.io/agent` (далее - `F/agent`)
  - `sds-replicated-volume.storage.deckhouse.io/controller` (далее - `F/controller`)

При удалении RVR, agent не удаляет ресурс из DRBD, и не снимает финализаторы,
пока есть хотя бы один финализатор, кроме `F/agent`.

### Цель 

Цель `rvr-finalizer-release-controller` - снимать финализатор `F/controller` с удаляемых rvr, когда 
кластер к этому готов.

Условие готовности (даже если `rv.metadata.deletionTimestamp!=nil`):
- удаляемые реплики не опубликованы (`rv.status.publishedOn`), при этом при удалении RV, удаляемыми
считаются все реплики (`len(rv.status.publishedOn)==0`)

В случае, когда RV не удаляется (`rv.metadata.deletionTimestamp==nil`), требуется
проверить дополнительные условия:
- количество rvr `rvr.status.conditions[type=Online].status == True`
(исключая ту, которую собираются удалить) больше, либо равно `rv.status.drbd.config.quorum`
- присутствует необходимое количество `rvr.spec.Type==Diskful && rvr.status.actualType==Diskful && rvr.status.conditions[type=IOReady].status==True && rvr.metadata.deletionTimestamp==nil` реплик, в
соответствии с `rsc.spec.replication`

### Вывод
 - удалить `rvr.metadata.finalizers[sds-replicated-volume.storage.deckhouse.io/controller]`

## `rv-finalizer-controller`

### Статус: [OK | priority: 5 | complexity: 1]

### Цель

Добавлять финализатор `sds-replicated-volume.storage.deckhouse.io/controller` на rv.

Снимать финализатор с rv, когда на нем есть `metadata.deletionTimestamp` и в
кластере нет rvr, привязанных к данному rv по `rvr.spec.replicatedVolumeName`.

### Вывод
-  добавляет и снимает финализатор `sds-replicated-volume.storage.deckhouse.io/controller` на rv

## `rv-delete-propagation-controller`

### Статус: [OK | priority: 5 | complexity: 1]

### Цель
Вызвать delete для всех rvr, у которых стоит `metadata.deletionTimestamp` на RV

### Вывод
 - удаляет `rvr`

## `rvr-missing-node-controller`

### Статус: [TBD | priority: 3 | complexity: 3]

### Цель 
Удаляет (без снятия финализатора) RVR с тех нод, которых больше нет в кластере.

### Триггер 
  - во время INIT/DELETE `corev1.Node`
    - когда Node больше нет в кластере

### Вывод 
  - delete rvr

## `llv-owner-reference-controller`

### Статус: [TBD | priority: 5 | complexity: 1]

### Цель 

Поддерживать `llv.metada.ownerReference`, указывающий на `rvr`.

Чтобы выставить правильные настройки, требуется использовать функцию `SetControllerReference` из пакета
`sigs.k8s.io/controller-runtime/pkg/controller/controllerutil`.

### Вывод 
 - `llv.metada.ownerReference`

## `rv-status-conditions-controller`
### Цель
### Вывод

## `rv-gc-controller`
### Цель
### Вывод

## `tie-breaker-removal-controller`
### Цель
### Вывод
