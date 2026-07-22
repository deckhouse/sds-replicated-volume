---
title: "Модуль sds-replicated-volume: примеры конфигурации"
description: "Использование и примеры работы sds-replicated-volume-controller."
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [системных требований](./readme.html#системные-требования-и-рекомендации).
Использование в других условиях возможно, но стабильная работа в таких случаях не гарантируется.
{{< /alert >}}

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse, останется только создать ReplicatedStoragePool и ReplicatedStorageClass по инструкции ниже.

## Конфигурация модуля

Конфигурацию выполняет контроллер `sds-replicated-volume-controller` с использованием пользовательских ресурсов [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и [ReplicatedStorageClass](./cr.html#replicatedstorageclass). Для создания Storage Pool требуется, чтобы на узлах кластера были заранее настроены [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) и LVM Thin Pool. Настройку LVM обеспечивает модуль [`sds-node-configurator`](/modules/sds-node-configurator/).

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [sds-node-configurator](/modules/sds-node-configurator/usage.html). В результате настройки в кластере окажутся ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup), которые необходимы для дальнейшей конфигурации.

### Работа с ресурсами ReplicatedStoragePool

#### Создание ресурса ReplicatedStoragePool

Чтобы создать Storage Pool, выполните следующие шаги:

- Для создания `Storage Pool` пользователь создаёт ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и заполняет поле `spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup).

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
- Все узлы должны иметь тип отличный от `CloudEphemeral` ([«Типы узлов»](/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#%D1%82%D0%B8%D0%BF%D1%8B-%D1%83%D0%B7%D0%BB%D0%BE%D0%B2)).

Информация о ходе и результатах работы контроллера доступна в поле `status` созданного ресурса `ReplicatedStoragePool`.

Затем `sds-replicated-volume-controller` обработает заданный пользователем ресурс `ReplicatedStoragePool` и создаст соответствующий `Storage Pool` в бэкенде. Имя создаваемого `Storage Pool` будет совпадать с именем созданного ресурса `ReplicatedStoragePool`. `Storage Pool` будет создан на узлах, указанных в ресурсах LVMVolumeGroup.

#### Обновление ресурса ReplicatedStoragePool

В список `spec.lvmVolumeGroups` можно добавлять новые LVMVolumeGroup (фактически — добавлять новые узлы в Storage Pool).

После внесения изменений `sds-replicated-volume-controller` провалидирует новую конфигурацию и при валидных данных выполнит необходимые операции по обновлению `Storage Pool` в бэкенде. Результаты операции также отобразятся в поле `status` ресурса `ReplicatedStoragePool`.

{{< alert level="warning" >}}
Поле `spec.type` ресурса `ReplicatedStoragePool` **неизменяемое**.
Контроллер не реагирует на внесенные пользователем изменения в поле `status` ресурса.
{{< /alert >}}

#### Удаление ресурса ReplicatedStoragePool

В настоящий момент `sds-replicated-volume-controller` никак не обрабатывает удаление ресурсов ReplicatedStoragePool.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в бэкенде.
Если пользователь воссоздаст удалённый ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений.

В поле `status.phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами ReplicatedStorageClass

#### Создание ресурса ReplicatedStorageClass

Для создания StorageClass в Kubernetes пользователь создаёт ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) и заполняет поле `spec`, указывая необходимые параметры. (Ручное создание StorageClass для CSI-драйвера replicated.csi.storage.deckhouse.io запрещено).

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

Пример ресурса для создания StorageClass c разрешёнными подключениями к данным по сети и без резервирования в кластере, где отсутствуют зоны (например, подходит для тестовых окружений):

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

Больше примеров с различными сценариями использования и схемами описаны [в документации](./layouts.html).

> Перед процессом создания StorageClass запустится процесс валидации предоставленной конфигурации.
> В случае обнаружения ошибок StorageClass создан не будет, а в поле `status` ресурса ReplicatedStorageClass отобразится информация об ошибке.

Результатом обработки ресурса ReplicatedStorageClass станет создание необходимого StorageClass в Kubernetes.

{{< alert level="warning" >}}
Все поля в `spec` ресурса ReplicatedStorageClass являются **неизменяемыми**.
{{< /alert >}}

Поле `status` будет обновляться контроллером `sds-replicated-volume-controller` для отображения информации о результатах проводимых операций.

#### Обновление ресурса ReplicatedStorageClass

Поменять параметры StorageClass, созданного через ресурс ReplicatedStorageClass, на данный момент **невозможно**.

#### Удаление ресурса ReplicatedStorageClass

Пользователь может удалить StorageClass в Kubernetes, удалив соответствующий ресурс ReplicatedStorageClass.
`sds-replicated-volume-controller` отреагирует на удаление ресурса и выполнит все необходимые операции для корректного удаления дочернего StorageClass.

> `sds-replicated-volume-controller` выполнит удаление дочернего StorageClass только в случае, если в поле `status.phase` ресурса ReplicatedStorageClass будет указано значение `Created`. В иных случаях будет удалён только ресурс ReplicatedStorageClass, а дочерний StorageClass затронут не будет.

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик, который учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома sds-replicated-volume.

Data locality настраивается параметром `volumeAccess` при создании ресурса ReplicatedStorageClass.
