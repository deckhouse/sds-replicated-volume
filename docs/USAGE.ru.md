---
title: "Модуль sds-replicated-volume: примеры конфигурации"
description: "Использование и примеры работы sds-replicated-volume-controller."
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [системных требований](./readme.html#системные-требования-и-рекомендации).
Использование в других условиях возможно, но стабильная работа в таких случаях не гарантируется.
{{< /alert >}}

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse создайте [ReplicatedStoragePool](#создание-ресурса-replicatedstoragepool) и [ReplicatedStorageClass](#создание-ресурса-replicatedstorageclass) по инструкции ниже.

## Конфигурация модуля

Конфигурацию выполняет контроллер `sds-replicated-volume-controller` с использованием пользовательских ресурсов [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и [ReplicatedStorageClass](./cr.html#replicatedstorageclass). Для создания Storage Pool заранее настройте на узлах кластера [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) и LVM Thin Pool. Настройку LVM обеспечивает модуль [`sds-node-configurator`](/modules/sds-node-configurator/).

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [sds-node-configurator](/modules/sds-node-configurator/usage.html). В результате настройки в кластере появятся ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup), необходимые для дальнейшей конфигурации.

### Работа с ресурсами ReplicatedStoragePool

#### Создание ресурса ReplicatedStoragePool

1. Создайте ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и заполните поле [`spec`](./cr.html#replicatedstoragepool-v1alpha1-spec), указав тип пула и используемые ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup).

   Пример ресурса для классических LVM-томов (Thick):

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: data
   spec:
     type: LVM
     lvmVolumeGroups:
     - name: lvg-1
     - name: lvg-2
   ```

   Пример ресурса для Thin-томов LVM:

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: thin-data
   spec:
     type: LVMThin
     lvmVolumeGroups:
       - name: lvg-3
         thinPoolName: thin-pool
       - name: lvg-4
         thinPoolName: thin-pool
   ```

1. Дождитесь валидации конфигурации контроллером перед работой с бэкендом. При ошибке проверьте причину в поле [`status`](./cr.html#replicatedstoragepool-v1alpha1-status).

   Для всех ресурсов [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup), указанных в [`spec`](./cr.html#replicatedstoragepool-v1alpha1-spec) ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool), должны соблюдаться следующие правила:

   - Они должны быть на разных узлах. Не указывайте несколько ресурсов LVMVolumeGroup, расположенных на одном и том же узле.
   - Все узлы должны иметь тип, отличный от `CloudEphemeral` ([«Типы узлов»](/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#%D1%82%D0%B8%D0%BF%D1%8B-%D1%83%D0%B7%D0%BB%D0%BE%D0%B2)).

1. Проверьте ход и результаты работы контроллера в поле [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) созданного ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool).

Контроллер `sds-replicated-volume-controller` обрабатывает ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и создаёт соответствующий Storage Pool в бэкенде. Имя создаваемого Storage Pool совпадает с именем ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool). Storage Pool создаётся на узлах, указанных в ресурсах [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup).

#### Обновление ресурса ReplicatedStoragePool

1. Добавьте новые ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) в список [`spec.lvmVolumeGroups`](./cr.html#replicatedstoragepool-v1alpha1-spec-lvmvolumegroups) (фактически — добавьте новые узлы в Storage Pool).

1. Дождитесь валидации новой конфигурации контроллером `sds-replicated-volume-controller`. При валидных данных контроллер обновит Storage Pool в бэкенде.

1. Проверьте результаты операции в поле [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool).

{{< alert level="warning" >}}
Поле [`spec.type`](./cr.html#replicatedstoragepool-v1alpha1-spec-type) ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool) **неизменяемое**. Контроллер не реагирует на изменения в поле [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) ресурса.
{{< /alert >}}

#### Удаление ресурса ReplicatedStoragePool

При необходимости удалите ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool).

{{< alert level="warning" >}}
В настоящий момент `sds-replicated-volume-controller` никак не обрабатывает удаление ресурсов [ReplicatedStoragePool](./cr.html#replicatedstoragepool). Удаление ресурса никак не затрагивает созданные по нему Storage Pool в бэкенде. Если воссоздать удалённый ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие Storage Pool уже созданы, и оставит их без изменений. В поле [`status.phase`](./cr.html#replicatedstoragepool-v1alpha1-status-phase) созданного ресурса будет отображено значение `Created`.
{{< /alert >}}

### Работа с ресурсами ReplicatedStorageClass

#### Создание ресурса ReplicatedStorageClass

1. Создайте ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) и заполните поле [`spec`](./cr.html#replicatedstorageclass-v1alpha1-spec), указав необходимые параметры. Не создавайте StorageClass для CSI-драйвера `replicated.csi.storage.deckhouse.io` вручную.

   Пример ресурса для создания StorageClass с использованием только локальных томов (запрещены подключения к данным по сети) и обеспечением высокой степени резервирования данных в кластере, состоящем из трех зон:

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: haclass
   spec:
     storagePool: storage-pool-name
     volumeAccess: Local
     reclaimPolicy: Delete
     topology: TransZonal
     zones:
     - zone-a
     - zone-b
     - zone-c
   ```

   Параметр [`replication`](./cr.html#replicatedstorageclass-v1alpha1-spec-replication) не указан, поскольку по умолчанию его значение устанавливается в `ConsistencyAndAvailability`, что соответствует требованиям высокой степени резервирования.

   Пример ресурса для создания StorageClass с разрешёнными подключениями к данным по сети и без резервирования в кластере, где отсутствуют зоны (например, подходит для тестовых окружений):

   ```yaml
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: testclass
   spec:
     replication: None
     storagePool: storage-pool-name
     reclaimPolicy: Delete
     topology: Ignored
   ```

   Больше примеров с различными сценариями использования и схемами описаны в [документации](./layouts.html).

{{< alert level="info" >}}
Перед процессом создания StorageClass запустится процесс валидации предоставленной конфигурации.
В случае обнаружения ошибок StorageClass создан не будет, а в поле `status` ресурса ReplicatedStorageClass отобразится информация об ошибке.
{{< /alert >}}

Результатом обработки ресурса ReplicatedStorageClass станет создание необходимого StorageClass в Kubernetes.

{{< alert level="warning" >}}
Большинство полей `spec` ресурса ReplicatedStorageClass **неизменяемы** после создания. У существующего ресурса можно менять только параметры репликации (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy` и `reclaimPolicy` (StorageClass при этом пересоздаётся с новой политикой); изменение любого другого поля (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector` и т. п.) при обновлении отклоняется.
{{< /alert >}}

Поле `status` будет обновляться контроллером `sds-replicated-volume-controller` для отображения информации о результатах проводимых операций.

#### Обновление ресурса ReplicatedStorageClass

Большинство полей `spec` неизменяемы после создания, и попытка их изменить отклоняется с ошибкой, называющей поле; для изменения такого поля (например `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) ресурс нужно пересоздать. Параметры репликации и `reclaimPolicy` изменяемы: правка `replication` позволяет выполнить миграцию r3→r2, описанную ниже, а правка `reclaimPolicy` заставляет модуль пересоздать StorageClass с новой политикой.

##### Миграция томов с трёх реплик (r3) на две реплики + tie-breaker (r2)

Изменение `spec.replication` у существующего ReplicatedStorageClass меняет целевой layout сразу **у всех** томов этого класса. Чтобы мигрировать с `ConsistencyAndAvailability` (три реплики данных, layout `3D`) на `Availability` (две реплики данных плюс diskless tie-breaker, layout `2D+1TB`):

1. Измените класс:

   ```shell
   d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

   `<RSC_NAME>` — имя ресурса ReplicatedStorageClass.

2. Контроллер мигрирует каждый том на месте: одна diskful-реплика ретайпится в tie-breaker (без полного ресинка и без переноса данных), её логический том освобождается. Прогресс по каждому тому виден через condition `MembershipLayoutConverged` и колонку `MembershipLayout`:

   ```shell
   d8 k get replicatedvolume -o wide
   d8 k get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}{end}{"\n"}'
   ```

   Том мигрирован, когда `MembershipLayoutConverged` = `True/Converged`, а `status.membershipLayout` = `2D+1TB`.

3. Прогресс по классу в целом виден через condition `ConfigurationRolledOut` и счётчики `status.volumes`:

   ```shell
   d8 k get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.volumes}{"\n"}'
   ```

   Раскатка завершена, когда `ConfigurationRolledOut` = `True`; это происходит ровно тогда, когда `status.volumes.pendingObservation` и `status.volumes.staleConfiguration` оба равны `0`.

   Равенства `aligned` и `total` ждать не нужно. В раскатке участвуют только тома, берущие конфигурацию у класса; том, переведённый в `spec.configurationMode: Manual`, несёт собственную конфигурацию, поэтому класс ничего ему не раскатывает и его не ждёт. При этом такие тома продолжают учитываться в `total`, и раскатка класса завершается при `aligned` меньше `total`.

**Требования.** Для layout `2D+1TB` кроме двух diskful-узлов нужен узел под tie-breaker: не менее 3 узлов для топологии `Ignored`, не менее 3 зон для `TransZonal` либо не менее 3 узлов в зоне тома для `Zonal`. Это те же требования, что и для `3D`, поэтому миграция r3→r2 их не повышает.

**Раскатка только на новые тома.** По умолчанию (`configurationRolloutStrategy.type: RollingUpdate`) правка конфигурации применяется ко всем томам класса. Чтобы она применялась только к вновь создаваемым томам, переключите стратегию на `NewVolumesOnly`:

```shell
d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"NewVolumesOnly","rollingUpdate":null}}}'
```

Тома, у которых конфигурация уже есть, сохраняют её. Такой том видит новую конфигурацию (класс не зависает в ожидании тома), но не применяет её, и репортит:

```shell
d8 k get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/NewerConfigurationHeld: ... has a newer configuration (generation N); the volume keeps its configuration (generation M) ...
```

Такие тома попадают в счётчик `status.volumes.staleConfiguration` класса, а `ConfigurationRolledOut` становится `False/ConfigurationRolloutDisabled`. Удержание намеренное и сохраняется, даже если удерживаемая конфигурация перестала соответствовать кластеру: чтобы выпустить том из этого состояния, переключите стратегию обратно на `RollingUpdate` (все удерживаемые тома раскатаются обычным путём) либо пересоздайте том. Переключение с `RollingUpdate` на `NewVolumesOnly` ничего не откатывает — уже применённая конфигурация остаётся применённой.

**Ограничение параллельности раскатки.** При `RollingUpdate` параметр `configurationRolloutStrategy.rollingUpdate.maxParallel` (по умолчанию `5`) задаёт, сколько томов класса мигрируют одновременно:

```shell
d8 k patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxParallel":2}}}}'
```

Тома, которым ещё нужна новая конфигурация, упорядочены по имени, и свободные слоты занимают первые из них — сразу все, то есть при `maxParallel: 2` первые два тома мигрируют параллельно. Слот освобождается, когда его том репортит `MembershipLayoutConverged=True/Converged`, и переходит к следующему имени в этом порядке. Ожидающие тома сохраняют собственную конфигурацию и репортят:

```shell
d8 k get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/ConfigurationRolloutInProgress: ... rolls its configuration (generation N) out to at most 2 volume(s) at a time ...
```

Ожидающие тома попадают в счётчик `status.volumes.staleConfiguration` класса, поэтому `ConfigurationRolledOut` остаётся `False/ConfigurationRolloutInProgress`, пока не мигрирует весь класс. Уменьшение `maxParallel` не останавливает уже мигрирующие тома — оно лишь не пускает в раскатку новые. Том, который не может сойтись на новой конфигурации (см. ограничения ниже), удерживает свой слот бессрочно, и в этом и состоит смысл параметра: он ограничивает не только скорость раскатки удачной правки, но и число томов, до которых доберётся неудачная.

**Ограничения.**

- Автоматического обратного пути нет: изменение `replication` в сторону большего числа реплик (r2→r3) репортится на каждом томе как `MembershipLayoutConverged=False/TransitionUnsupported` и не выполняет никаких действий — требуется ручная разборка.
- Откат правки, пока том ещё мигрирует, не отменяет уже запущенный перевод реплики в tie-breaker, и такой том больше не вернётся в `Converged` сам. В зависимости от момента отката том останется либо в раскладке `2D+1TB` при желаемой `3D` (`MembershipLayoutConverged=False/TransitionUnsupported`), либо с репликой, у которой `spec.type` застрял в значении `TieBreaker`, тогда как раскладка по-прежнему `3D` (`MembershipLayoutConverged=False/Converging`, имя реплики указано в сообщении condition). Данные не теряются ни в одном из случаев. Во втором случае реплику нужно восстановить одним патчем: вернуть `spec.type` в `Diskful` и одновременно вернуть поля backing volume (`spec.lvmVolumeGroupName`, а для thin-пула ещё и `spec.lvmVolumeGroupThinPoolName`), взяв значения из `status.datamesh.members` тома.
- `eligibleNodesConflictResolutionStrategy.rollingRepair.maxParallel` принимается, но не реализован: перенос томов с узлов, переставших быть eligible, не ограничивается по параллельности. Это другой параметр, не тот `maxParallel` из конфигурационной раскатки выше — он-то как раз работает.

#### Удаление ресурса ReplicatedStorageClass

1. Удалите ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass), чтобы удалить связанный StorageClass в Kubernetes.

1. Дождитесь, пока `sds-replicated-volume-controller` обнаружит удаление и выполнит все необходимые операции для корректного удаления дочернего StorageClass.

{{< alert level="warning" >}}
`sds-replicated-volume-controller` выполнит удаление дочернего StorageClass только в случае, если в поле `status.phase` ресурса ReplicatedStorageClass будет указано значение `Created`. В иных случаях будет удалён только ресурс ReplicatedStorageClass, а дочерний StorageClass затронут не будет.
{{< /alert >}}

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик, который учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома `sds-replicated-volume`.

Data locality настраивается параметром [`volumeAccess`](./cr.html#replicatedstorageclass-v1alpha1-spec-volumeaccess) при создании ресурса [ReplicatedStorageClass](./cr.html#replicatedstorageclass).
