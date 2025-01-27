---
title: "Модуль sds-replicated-volume: примеры конфигурации"
description: "Использование и примеры работы sds-replicated-volume-controller."
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [требований](./readme.html#системные-требования-и-рекомендации).
Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse, останется только создать ReplicatedStoragePool и ReplicatedStorageClass по инструкции ниже.

## Конфигурация sds-replicated-volume

Конфигурация осуществляется `sds-replicated-volume-controller'ом` через [пользовательских ресурсов](./cr.html): `ReplicatedStoragePool` и `ReplicatedStorageClass`. Для создания `Storage Pool` потребуются настроенные на узлах кластера `LVM Volume Group` и `LVM Thin-pool`. Настройка `LVM` осуществляется модулем [sds-node-configurator](../../sds-node-configurator/stable/).

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [sds-node-configurator](../../sds-node-configurator/stable/usage.html). В результате настройки в кластере окажутся ресурсы [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup), которые необходимы для дальнейшей конфигурации.

### Работа с ресурсами `ReplicatedStoragePool`

#### Создание ресурса `ReplicatedStoragePool`

- Для создания `Storage Pool` пользователь создает ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool) и заполняет поле `spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup).

- Пример ресурса для классических LVM-томов (Thick):

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

- Пример ресурса для Thin-томов LVM:

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

Для всех ресурсов `LVMVolumeGroup`, указанных в `spec` ресурса `ReplicatedStoragePool` должны быть соблюдены следующие правила:
 - Они должны быть на разных узлах. Запрещено указывать несколько ресурсов `LVMVolumeGroup`, которые расположены на одном и том же узле.
 - Все узлы должны иметь тип отличный от `CloudEphemeral` (см. [Типы узлов](https://deckhouse.ru/products/kubernetes-platform/documentation/v1/modules/040-node-manager/#%D1%82%D0%B8%D0%BF%D1%8B-%D1%83%D0%B7%D0%BB%D0%BE%D0%B2))

Информацию о ходе работы контроллера и ее результатах можно посмотреть в поле `status` созданного ресурса `ReplicatedStoragePool`.

Результатом обработки ресурса `ReplicatedStoragePool` станет создание необходимого `Storage Pool` в бэкенде. Имя созданного `Storage Pool` будет соответствовать имени созданного ресурса `ReplicatedStoragePool`. Узлы, на которых будет создан `Storage Pool`, будут взяты из ресурсов LVMVolumeGroup.

#### Обновление ресурса `ReplicatedStoragePool`

Пользователь имеет возможность добавлять новые `LVMVolumeGroup` в список `spec.lvmVolumeGroups` (фактически добавить новые узлы в Storage Pool).

После внесения изменений в ресурс, `sds-replicated-volume-controller` провалидирует новую конфигурацию и в случае валидных данных выполнит необходимые операции по обновлению `Storage Pool` в бэкенде. Результаты данной операции также будут отображены в поле `status` ресурса `ReplicatedStoragePool`.

> Обратите внимание, что поле `spec.type` ресурса `ReplicatedStoragePool` **неизменяемое**.
>
> Контроллер не реагирует на внесенные пользователем изменения в поле `status` ресурса.

#### Удаление ресурса `ReplicatedStoragePool`

В настоящий момент `sds-replicated-volume-controller` никак не обрабатывает удаление ресурсов `ReplicatedStoragePool`.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в бэкенде. Если пользователь воссоздаст удаленный ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `status.phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами `ReplicatedStorageClass`

#### Создание ресурса `ReplicatedStorageClass`

- Для создания `StorageClass` в `Kubernetes` пользователь создает ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) и заполняет поле `spec`, указывая необходимые параметры. (Ручное создание StorageClass для CSI-драйвера replicated.csi.storage.deckhouse.io запрещено).

- Пример ресурса для создания `StorageClass` c использованием только локальных томов (запрещены подключения к данным по сети) и обеспечением высокой степени резервирования данных в кластере, состоящем из трех зон:

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

- Пример ресурса для создания `StorageClass` c разрешенными подключениями к данным по сети и без резервирования в кластере, где отсутствуют зоны (например, подходит для тестовых окружений):

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

- Больше примеров с различными сценариями использования и схемами [можно найти здесь](./layouts.html)

> Перед процессом непосредственно создания `StorageClass` запустится процесс валидации предоставленной конфигурации.
> В случае обнаружения ошибок `StorageClass` создан не будет, а в поле `status` ресурса `ReplicatedStorageClass` отобразится информация об ошибке.

Результатом обработки ресурса `ReplicatedStorageClass` станет создание необходимого `StorageClass` в `Kubernetes`.

> Обратите внимание, что все поля, кроме поля `isDefault` в поле `spec` ресурса `ReplicatedStorageClass`, являются **неизменяемым**.

Поле `status` будет обновляться `sds-replicated-volume-controller'ом` для отображения информации о результатах проводимых операций.

#### Обновление ресурса `ReplicatedStorageClass`

`sds-replicated-volume-controller` в настоящий момент поддерживает только изменение поля `isDefault`. Поменять остальные параметры
`StorageClass`, созданного через ресурс `ReplicatedStorageClass`, на данный момент **невозможно**.

#### Удаление ресурса `ReplicatedStorageClass`

Пользователь может удалить `StorageClass` в `Kubernetes`, удалив соответствующий ресурс `ReplicatedStorageClass`.
`sds-replicated-volume-controller` отреагирует на удаление ресурса и выполнит все необходимые операции для корректного удаления дочернего `StorageClass`.

> `sds-replicated-volume-controller` выполнит удаление дочернего `StorageClass` только в случае, если в поле `status.phase` ресурса `ReplicatedStorageClass` будет указано значение `Created`. В иных случаях будет удалён только ресурс `ReplicatedStorageClass`, а дочерний `StorageClass` затронут не будет.

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома sds-replicated-volume.

Data locality настраивается параметром `volumeAccess` при создании ресурса `ReplicatedStorageClass`.
