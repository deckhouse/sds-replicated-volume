---
title: "Модуль sds-replicated-volume: примеры конфигурации"
description: "Использование и примеры работы sds-replicated-volume-controller."
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [требований](./readme.html#системные-требования-и-рекомендации).
Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse, останется только создать ReplicatedStoragePool и ReplicatedStorageClass по инструкции ниже.

## Конфигурация модуля

Конфигурацию выполняет контроллер `sds-replicated-volume-controller` с использованием пользовательских ресурсов [ReplicatedStoragePool](/modules/sds-replicated-volume/cr.html#replicatedstoragepool) и [ReplicatedStorageClass](/modules/sds-replicated-volume/cr.html#replicatedstorageclass). Для создания Storage Pool требуется, чтобы на узлах кластера были заранее настроены [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) и LVM Thin Pool. Настройку LVM обеспечивает модуль [`sds-node-configurator`](/modules/sds-node-configurator/).

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [sds-node-configurator](/modules/sds-node-configurator/resources.html). В результате настройки в кластере окажутся ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup), которые необходимы для дальнейшей конфигурации.

### Работа с ресурсами ReplicatedStoragePool

#### Создание ресурса ReplicatedStoragePool

- Для создания `Storage Pool` пользователь создает ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и заполняет поле `spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup).

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

Перед работой с бэкендом контроллер провалидирует предоставленную ему конфигурацию и в случае ошибки предоставит информацию о причинах неудачи.

Для всех ресурсов LVMVolumeGroup, указанных в `spec` ресурса ReplicatedStoragePool должны быть соблюдены следующие правила:

- Они должны быть на разных узлах. Запрещено указывать несколько ресурсов LVMVolumeGroup, которые расположены на одном и том же узле.
- Все узлы должны иметь тип отличный от `CloudEphemeral` (см. [Типы узлов](https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#%D1%82%D0%B8%D0%BF%D1%8B-%D1%83%D0%B7%D0%BB%D0%BE%D0%B2))

Результатом обработки ресурса `ReplicatedStoragePool` станет создание необходимого `Storage Pool` в бэкенде. Имя созданного `Storage Pool` будет соответствовать имени созданного ресурса `ReplicatedStoragePool`. Узлы, на которых будет создан `Storage Pool`, будут взяты из ресурсов LVMVolumeGroup.

Результатом обработки ресурса ReplicatedStoragePool станет создание необходимого Storage Pool в бэкенде LINSTOR. Имя созданного Storage Pool будет соответствовать имени созданного ресурса ReplicatedStoragePool. Узлы, на которых будет создан Storage Pool, будут взяты из ресурсов LVMVolumeGroup.

#### Обновление ресурса ReplicatedStoragePool

После внесения изменений в ресурс, `sds-replicated-volume-controller` провалидирует новую конфигурацию и в случае валидных данных выполнит необходимые операции по обновлению `Storage Pool` в бэкенде. Результаты данной операции также будут отображены в поле `status` ресурса `ReplicatedStoragePool`.

> Обратите внимание, что поле `spec.type` ресурса `ReplicatedStoragePool` **неизменяемое**.
>
> Контроллер не реагирует на внесенные пользователем изменения в поле `status` ресурса.

#### Удаление ресурса ReplicatedStoragePool

В настоящий момент `sds-replicated-volume-controller` никак не обрабатывает удаление ресурсов ReplicatedStoragePool.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в бэкенде. Если пользователь воссоздаст удаленный ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `status.phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами ReplicatedStorageClass

#### Создание ресурса ReplicatedStorageClass

Для создания StorageClass в Kubernetes пользователь создает ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) и заполняет поле `spec`, указывая необходимые параметры. (Ручное создание StorageClass для CSI-драйвера replicated.csi.storage.deckhouse.io запрещено).

Пример ресурса для создания StorageClass c использованием только локальных томов (запрещены подключения к данным по сети) и обеспечением высокой степени резервирования данных в кластере, состоящем из трех зон:

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

Параметр `replication` не указан, поскольку по умолчанию его значение устанавливается в `ConsistencyAndAvailability`, что соответствует требованиям высокой степени резервирования.

Пример ресурса для создания StorageClass c разрешенными подключениями к данным по сети и без резервирования в кластере, где отсутствуют зоны (например, подходит для тестовых окружений):

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

Больше примеров с различными сценариями использования и схемами описаны [в документации](./layouts.html)

> Перед процессом создания StorageClass запустится процесс валидации предоставленной конфигурации.
> В случае обнаружения ошибок StorageClass создан не будет, а в поле `status` ресурса ReplicatedStorageClass отобразится информация об ошибке.

Результатом обработки ресурса ReplicatedStorageClass станет создание необходимого StorageClass в Kubernetes.

> Обратите внимание, что большинство полей `spec` ресурса ReplicatedStorageClass являются **неизменяемыми** после создания. Изменить у существующего ресурса можно только параметры репликации (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy`, а также `reclaimPolicy` (StorageClass при этом пересоздаётся с новой политикой); изменение любого другого поля (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector` и т. д.) при обновлении будет отклонено.

Поле `status` будет обновляться `sds-replicated-volume-controller'ом` для отображения информации о результатах проводимых операций.

#### Обновление ресурса ReplicatedStorageClass

Большинство полей `spec` неизменяемы после создания, и попытка их изменить отклоняется с ошибкой, называющей поле; для изменения такого поля (например `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) ресурс нужно пересоздать. Параметры репликации и `reclaimPolicy` изменяемы: правка `replication` позволяет выполнить миграцию r3→r2, описанную ниже, а правка `reclaimPolicy` заставляет модуль пересоздать StorageClass с новой политикой.

##### Миграция томов с трёх реплик (r3) на две реплики + tie-breaker (r2)

Изменение `spec.replication` у существующего ReplicatedStorageClass меняет целевой layout сразу **у всех** томов этого класса. Чтобы мигрировать с `ConsistencyAndAvailability` (три реплики данных, layout `3D`) на `Availability` (две реплики данных плюс diskless tie-breaker, layout `2D+1TB`):

1. Измените класс:

   ```shell
   kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

2. Контроллер мигрирует каждый том на месте: одна diskful-реплика ретайпится в tie-breaker (без полного ресинка и без переноса данных), её логический том освобождается. Прогресс по каждому тому виден через condition `MembershipLayoutConverged` и колонку `MembershipLayout`:

   ```shell
   kubectl get replicatedvolume -o wide
   kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}{end}{"\n"}'
   ```

   Том мигрирован, когда `MembershipLayoutConverged` = `True/Converged`, а `status.membershipLayout` = `2D+1TB`.

3. Прогресс по классу в целом виден через condition `ConfigurationRolledOut` и счётчики `status.volumes`:

   ```shell
   kubectl get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.volumes}{"\n"}'
   ```

   Раскатка завершена, когда `ConfigurationRolledOut` = `True`; это происходит ровно тогда, когда `status.volumes.pendingObservation` и `status.volumes.staleConfiguration` оба равны `0`.

   Равенства `aligned` и `total` ждать не нужно. В раскатке участвуют только тома, берущие конфигурацию у класса; том, переведённый в `spec.configurationMode: Manual`, несёт собственную конфигурацию, поэтому класс ничего ему не раскатывает и его не ждёт. При этом такие тома продолжают учитываться в `total`, и раскатка класса завершается при `aligned` меньше `total`.

**Требования.** Для layout `2D+1TB` кроме двух diskful-узлов нужен узел под tie-breaker: не менее 3 узлов для топологии `Ignored`, не менее 3 зон для `TransZonal` либо не менее 3 узлов в зоне тома для `Zonal`. Это те же требования, что и для `3D`, поэтому миграция r3→r2 их не повышает.

**Раскатка только на новые тома.** По умолчанию (`configurationRolloutStrategy.type: RollingUpdate`) правка конфигурации применяется ко всем томам класса. Чтобы она применялась только к вновь создаваемым томам, переключите стратегию на `NewVolumesOnly`:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"NewVolumesOnly","rollingUpdate":null}}}'
```

Тома, у которых конфигурация уже есть, сохраняют её. Такой том видит новую конфигурацию (класс не зависает в ожидании тома), но не применяет её, и репортит:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/NewerConfigurationHeld: ... has a newer configuration (generation N); the volume keeps its configuration (generation M) ...
```

Такие тома попадают в счётчик `status.volumes.staleConfiguration` класса, а `ConfigurationRolledOut` становится `False/ConfigurationRolloutDisabled`. Удержание намеренное и сохраняется, даже если удерживаемая конфигурация перестала соответствовать кластеру: чтобы выпустить том из этого состояния, переключите стратегию обратно на `RollingUpdate` (все удерживаемые тома раскатаются обычным путём) либо пересоздайте том. Переключение с `RollingUpdate` на `NewVolumesOnly` ничего не откатывает — уже применённая конфигурация остаётся применённой.

**Ограничение параллельности раскатки.** При `RollingUpdate` параметр `configurationRolloutStrategy.rollingUpdate.maxParallel` (по умолчанию `5`) задаёт, сколько томов класса мигрируют одновременно:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxParallel":2}}}}'
```

Тома, которым ещё нужна новая конфигурация, упорядочены по имени, и свободные слоты занимают первые из них — сразу все, то есть при `maxParallel: 2` первые два тома мигрируют параллельно. Слот освобождается, когда его том репортит `MembershipLayoutConverged=True/Converged`, и переходит к следующему имени в этом порядке. Ожидающие тома сохраняют собственную конфигурацию и репортят:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/ConfigurationRolloutInProgress: ... rolls its configuration (generation N) out to at most 2 volume(s) at a time ...
```

Ожидающие тома попадают в счётчик `status.volumes.staleConfiguration` класса, поэтому `ConfigurationRolledOut` остаётся `False/ConfigurationRolloutInProgress`, пока не мигрирует весь класс. Уменьшение `maxParallel` не останавливает уже мигрирующие тома — оно лишь не пускает в раскатку новые. Том, который не может сойтись на новой конфигурации (см. ограничения ниже), удерживает свой слот бессрочно, и в этом и состоит смысл параметра: он ограничивает не только скорость раскатки удачной правки, но и число томов, до которых доберётся неудачная.

**Ограничения.**

- Автоматического обратного пути нет: изменение `replication` в сторону большего числа реплик (r2→r3) репортится на каждом томе как `MembershipLayoutConverged=False/TransitionUnsupported` и не выполняет никаких действий — требуется ручная разборка.
- Откат правки, пока том ещё мигрирует, не отменяет уже запущенный перевод реплики в tie-breaker, и такой том больше не вернётся в `Converged` сам. В зависимости от момента отката том останется либо в раскладке `2D+1TB` при желаемой `3D` (`MembershipLayoutConverged=False/TransitionUnsupported`), либо с репликой, у которой `spec.type` застрял в значении `TieBreaker`, тогда как раскладка по-прежнему `3D` (`MembershipLayoutConverged=False/Converging`, имя реплики указано в сообщении condition). Данные не теряются ни в одном из случаев. Во втором случае реплику нужно восстановить одним патчем: вернуть `spec.type` в `Diskful` и одновременно вернуть поля backing volume (`spec.lvmVolumeGroupName`, а для thin-пула ещё и `spec.lvmVolumeGroupThinPoolName`), взяв значения из `status.datamesh.members` тома.
- `eligibleNodesConflictResolutionStrategy.rollingRepair.maxParallel` принимается, но не реализован: перенос томов с узлов, переставших быть eligible, не ограничивается по параллельности. Это другой параметр, не тот `maxParallel` из конфигурационной раскатки выше — он-то как раз работает.

#### Удаление ресурса ReplicatedStorageClass

Пользователь может удалить StorageClass в Kubernetes, удалив соответствующий ресурс ReplicatedStorageClass.
`sds-replicated-volume-controller` отреагирует на удаление ресурса и выполнит все необходимые операции для корректного удаления дочернего StorageClass.

> `sds-replicated-volume-controller` выполнит удаление дочернего StorageClass только в случае, если в поле `status.phase` ресурса ReplicatedStorageClass будет указано значение `Created`. В иных случаях будет удалён только ресурс ReplicatedStorageClass, а дочерний StorageClass затронут не будет.

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома sds-replicated-volume.

Data locality настраивается параметром `volumeAccess` при создании ресурса ReplicatedStorageClass.
