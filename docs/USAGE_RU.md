---
title: "Модуль SDS-DRDB: примеры конфигурации"
description: "Использование и примеры работы sds-drbd-controller."
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только в следующих случаях:
- при использовании стоковых ядер, поставляемых вместе с [поддерживаемыми дистрибутивами](https://deckhouse.ru/documentation/v1/supported_versions.html#linux);
- при использовании сети 10Gbps.

Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

После включения модуля кластер автоматически настраивается на использование LINSTOR и остается только сконфигурировать хранилище.

## Конфигурация хранилища LINSTOR

Конфигурация хранилища `LINSTOR` в `Deckhouse` осуществляется `sds-drbd-controller'ом` посредством создания [пользовательских ресурсов](./cr.html): `DRBDStoragePool` и `DRBDStorageClass`. Для создания `Storage Pool` потребуются настроенные на узлах кластера `LVM Volume Group` и `LVM Thin-pool`. Настройка `LVM` осуществляется модулем [SDS-Node-Configurator](../../sds-node-configurator/stable/).

### Настройка LVM

Примеры конфигурации можно найти в документации модуля [SDS-Node-Configurator](../../sds-node-configurator/stable/usage.html)

### Работа с ресурсами `DRBDStoragePool`

#### Создание ресурса `DRBDStoragePool`

- Для создания `Storage Pool` на определеных узлах в `LINSTOR` пользователь вручную создает ресурс [DRBDStoragePool](./cr.html#drbdstoragepool) и заполняет поле `Spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup). 

- Пример ресурса для thick LVM:

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

- Пример ресурса для thin LVM:

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


> Внимание! Все ресурсы `LVMVolumeGroup`, указанные в `Spec` ресурса `DRBDStoragePool`, должны быть на разных узлах. (Запрещено указывать несколько ресурсов `LVMVolumeGroup`, которые расположены на одном и том же узле.)

Результатом обработки пользовательского ресурса `DRBDStoragePool` станет создание необходимого `Storage Pool` в `LINSTOR`.

> Имя созданного `Storage Pool` будет соответствовать имени созданного ресурса `DRBDStoragePool`.
> Узлы, на которых будет создан `Storage Pool`, будут взяты из ресурсов LVMVolumeGroup.

Информацию о ходе выполнения работы контроллера и ее результатах можно посмотреть в поле `Status` созданного ресурса `DRBDStoragePool`.

> Перед фактической работой с `LINSTOR` контроллер провалидирует предоставленную ему конфигурацию и в случае ошибки предоставит информацию о причинах неудачи.
> Невалидные `Storage Pool'ы` не будут созданы в `LINSTOR`.
> 
> В случае возникновения ошибки при создании `Storage Pool` в `LINSTOR` контроллер удалит за собой невалидный `Storage Pool` в `LINSTOR`.

#### Обновление ресурса `DRBDStoragePool`

Пользователь имеет возможность обновить существующую конфигурацию `Storage Pool` в `LINSTOR`. Для этого пользователю необходимо внести желаемые изменения в поле `Spec` соответствующего ресурса.

После внесения изменений в ресурс `sds-drbd-controller` провалидирует новую конфигурацию и в случае валидных данных выполнит необходимые операции по обновлению `Storage Pool` в `LINSTOR`. Результаты данной операции также будут отображены в поле `Status` ресурса `DRBDStoragePool`.

> Обратите внимание, что поле `Spec.Type` ресурса `DRBDStoragePool` **неизменяемое**. 
> 
> Контроллер не реагирует на внесенные пользователем изменения в поле `Status` ресурса.

#### Удаление ресурса `DRBDStoragePool`
В настоящий момент `sds-drbd-controller` никак не обрабатывает удаление ресурсов `DRBDStoragePool`.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в `LINSTOR`. Если пользователь воссоздаст удаленный ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `Status.Phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами `DRBDStorageClass`

#### Создание ресурса `DRBDStorageClass`

- Для создания `StorageClass` в `Kubernetes` пользователь вручную создает ресурс [DRBDStorageClass](./cr.html#drbdstorageclass) и заполняет поле `Spec`, указывая необходимые параметры.

- Пример ресурса для создания `StorageClass` c использованием только локальных томов (запрещены подключения к данным по сети) и обеспечением высокой степени резервирования данных в кластере, состоящем из трех зон:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
metadata:
  name: haclass
spec:
  storagePool: storagePoolName
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
  storagePool: storagePoolName
  reclaimPolicy: Delete
  topology: Ignored
```

- Больше примеров с различными сценариями использования и схемами [можно найти здесь](./layouts.html)

> Перед процессом непосредственно создания `StorageClass` запустится процесс валидации предоставленной конфигурации. 
> В случае обнаружения ошибок `StorageClass` создан не будет, а в поле `Status` ресурса `DRBDStorageClass` отобразится информация об ошибке.

Результатом обработки пользовательского ресурса `DRBDStorageClass` станет создание необходимого `StorageClass` в `Kubernetes`. 

> Обратите внимание, что все поля, кроме поля `isDefault` в поле `spec` ресурса `DRBDStorageClass`, являются **неизменяемым**.
Поле `Status` будет обновляться `sds-drbd-controller'ом` для отображения информации о результатах проводимых операций.

#### Обновление ресурса `DRBDStorageClass`
`sds-drbd-controller` в настоящий момент поддерживает только изменение поля `isDefault`. Поменять остальные параметры
`StorageClass`, созданного через ресурс `DRBDStorageClass`, на данный момент **невозможно**.

#### Удаление ресурса `DRBDStorageClass`
Пользователь может удалить `StorageClass` в `Kubernetes`, удалив соответствующий ресурс `DRBDStorageClass`. 
`sds-drbd-controller` отреагирует на удаление ресурса и выполнит все необходимые операции для корректного удаления связанного `StorageClass`.

> `sds-drbd-controller` выполнит удаление связанного с ресурсом `StorageClass` только в случае, если в поле `status.Phase`
> ресурса `DRBDStorageClass` будет указано значение `Created`. В иных случаях контроллер удалять `StorageClass` не будет. 
> Ресурс при этом также **не будет** удален.

## Дополнительные возможности для приложений, использующих хранилище LINSTOR

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль linstor предоставляет специальный планировщик `linstor`, который учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально.
  
Любой под, использующий тома linstor, будет автоматически настроен на использование планировщика `linstor`.

Data locality настраивается параметром `volumeAccess` при создании пользовательского ресурса `DRBDStorageClass`.
