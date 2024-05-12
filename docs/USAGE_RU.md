---
title: "Модуль sds-drbd: примеры конфигурации"
description: "Использование и примеры работы sds-drbd-controller."
---

{% alert level="danger" %}
Текущая версия модуля устарела и больше не поддерживается. Переключитесь на использование модуля [sds-replicated-volume](https://deckhouse.ru/modules/sds-replicated-volume/stable/).
{% endalert %}

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только в следующих случаях:
- при использовании стоковых ядер, поставляемых вместе с [поддерживаемыми дистрибутивами](https://deckhouse.ru/documentation/v1/supported_versions.html#linux);
- при использовании сети 10Gbps.

Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

После включения модуля `sds-drbd` в конфигурации Deckhouse ваш кластер будет автоматически настроен на использование бэкенда `LINSTOR`. Останется только создать пулы хранения и StorageClass по инструкции ниже.

## Конфигурация бэкенда LINSTOR

Конфигурация `LINSTOR` в `Deckhouse` осуществляется `sds-drbd-controller'ом` посредством создания [пользовательских ресурсов](./cr.html): `DRBDStoragePool` и `DRBDStorageClass`. Для создания `Storage Pool` потребуются настроенные на узлах кластера `LVM Volume Group` и `LVM Thin-pool`. Настройка `LVM` осуществляется модулем [sds-node-configurator](../../sds-node-configurator/stable/).

> **Внимание!** Непосредственная конфигурация бэкенда `LINSTOR` пользователем запрещена.

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [sds-node-configurator](../../sds-node-configurator/stable/usage.html). В результате настройки в кластере окажутся ресурсы [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup), которые необходимы для дальнейшей конфигурации.

### Работа с ресурсами `DRBDStoragePool`

#### Создание ресурса `DRBDStoragePool`

- Для создания `Storage Pool` на определеных узлах в `LINSTOR` пользователь создает ресурс [DRBDStoragePool](./cr.html#drbdstoragepool) и заполняет поле `spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup).

- Пример ресурса для классических LVM-томов (Thick):

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStoragePool
metadata:
  name: data
spec:
  type: LVM
  lvmvolumegroups:
    - name: lvg-1
    - name: lvg-2
```

- Пример ресурса для Thin-томов LVM:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStoragePool
metadata:
  name: thin-data
spec:
  type: LVMThin
  lvmvolumegroups:
    - name: lvg-3
      thinpoolname: thin-pool
    - name: lvg-4
      thinpoolname: thin-pool
```

> Внимание! Все ресурсы `LVMVolumeGroup`, указанные в `spec` ресурса `DRBDStoragePool`, должны быть на разных узлах. (Запрещено указывать несколько ресурсов `LVMVolumeGroup`, которые расположены на одном и том же узле).

Результатом обработки ресурса `DRBDStoragePool` станет создание необходимого `Storage Pool` в бэкенде `LINSTOR`.

> Имя созданного `Storage Pool` будет соответствовать имени созданного ресурса `DRBDStoragePool`.
>
> Узлы, на которых будет создан `Storage Pool`, будут взяты из ресурсов LVMVolumeGroup.

Информацию о ходе работы контроллера и ее результатах можно посмотреть в поле `status` созданного ресурса `DRBDStoragePool`.

> Перед фактической работой с `LINSTOR` контроллер провалидирует предоставленную ему конфигурацию и в случае ошибки предоставит информацию о причинах неудачи.
>
> Невалидные `Storage Pool'ы` не будут созданы в `LINSTOR`.

#### Обновление ресурса `DRBDStoragePool`

Пользователь имеет возможность добавлять новые `LVMVolumeGroup` в список `spec.lvmVolumeGroups` (фактически добавить новые узлы в Storage Pool).

После внесения изменений в ресурс, `sds-drbd-controller` провалидирует новую конфигурацию и в случае валидных данных выполнит необходимые операции по обновлению `Storage Pool` в бэкенде `LINSTOR`. Результаты данной операции также будут отображены в поле `status` ресурса `DRBDStoragePool`.

> Обратите внимание, что поле `spec.type` ресурса `DRBDStoragePool` **неизменяемое**.
>
> Контроллер не реагирует на внесенные пользователем изменения в поле `status` ресурса.

#### Удаление ресурса `DRBDStoragePool`

В настоящий момент `sds-drbd-controller` никак не обрабатывает удаление ресурсов `DRBDStoragePool`.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в бэкенде `LINSTOR`. Если пользователь воссоздаст удаленный ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `status.phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами `DRBDStorageClass`

#### Создание ресурса `DRBDStorageClass`

- Для создания `StorageClass` в `Kubernetes` пользователь создает ресурс [DRBDStorageClass](./cr.html#drbdstorageclass) и заполняет поле `spec`, указывая необходимые параметры. (Ручное создание StorageClass для CSI-драйвера drbd.csi.storage.deckhouse.io запрещено).

- Пример ресурса для создания `StorageClass` c использованием только локальных томов (запрещены подключения к данным по сети) и обеспечением высокой степени резервирования данных в кластере, состоящем из трех зон:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
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
kind: DRBDStorageClass
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
> В случае обнаружения ошибок `StorageClass` создан не будет, а в поле `status` ресурса `DRBDStorageClass` отобразится информация об ошибке.

Результатом обработки ресурса `DRBDStorageClass` станет создание необходимого `StorageClass` в `Kubernetes`.

> Обратите внимание, что все поля, кроме поля `isDefault` в поле `spec` ресурса `DRBDStorageClass`, являются **неизменяемым**.

Поле `status` будет обновляться `sds-drbd-controller'ом` для отображения информации о результатах проводимых операций.

#### Обновление ресурса `DRBDStorageClass`

`sds-drbd-controller` в настоящий момент поддерживает только изменение поля `isDefault`. Поменять остальные параметры
`StorageClass`, созданного через ресурс `DRBDStorageClass`, на данный момент **невозможно**.

#### Удаление ресурса `DRBDStorageClass`

Пользователь может удалить `StorageClass` в `Kubernetes`, удалив соответствующий ресурс `DRBDStorageClass`.
`sds-drbd-controller` отреагирует на удаление ресурса и выполнит все необходимые операции для корректного удаления дочернего `StorageClass`.

> `sds-drbd-controller` выполнит удаление дочернего `StorageClass` только в случае, если в поле `status.phase` ресурса `DRBDStorageClass` будет указано значение `Created`. В иных случаях будет удалён только ресурс `DRBDStorageClass`, а дочерний `StorageClass` затронут не будет.

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома sds-drbd.

Data locality настраивается параметром `volumeAccess` при создании ресурса `DRBDStorageClass`.
