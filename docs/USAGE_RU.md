---
title: "Модуль SDS-DRDB: примеры конфигурации"
description: Использование и примеры работы sds-drbd-controller.
---
{% alert level="warning" %}
Работоспособность модуля гарантируется только в следующих случаях:
- при использовании стоковых ядер, поставляемых вместе с [поддерживаемыми дистрибутивами](../../supported_versions.html#linux);
- при использовании сети 10Gbps.

Работоспособность модуля в других условиях возможна, но не гарантируется.
{% endalert %}

После включения модуля кластер автоматически настраивается на использование LINSTOR и остается только сконфигурировать хранилище.

## Конфигурация хранилища LINSTOR

Конфигурация хранилища `LINSTOR` в `Deckhouse` осуществляется `sds-drbd-controller'ом` посредством создания [пользовательских ресурсов](ссылка на ресурсы): `DRBDStoragePool` и `DRBDStorageClass`.

- Для создания `Storage Pool` в `LINSTOR` пользователь вручную [создает](#создание-drbdstoragepool-ресурса) `DRBDStoragePool`-ресурс и заполняет поле `Spec`, указывая тип пула и используемые ресурсы [LVMVolumeGroup](ссылка на ресурс).

- Для создания `StorageClass` в `Kubernetes` пользователь вручную [создает](#создание-ресурса-drbdstorageclass) `DRBDStorageClass` и заполняет поле `Spec`, указывая необходимые параметры, а также используемые `DRBDStoragePool`-ресурсы.

## Описание работы sds-drbd-controller

Контроллер работает с двумя типами ресурсов:

### Работа с ресурсами [DRBDStoragePool]()

#### Создание `DRBDStoragePool`-ресурса
`DRBDStoragePool`-ресурс позволяет создать `Storage Pool` необходимой конфигурации в `LINSTOR` на основе используемых ресурсов [LVMVolumeGroup](ссылка на ресурс).
Для этого пользователю необходимо вручную создать ресурс и заполнить поле `Spec` (то есть желаемое состояние `Storage Pool` в `LINSTOR`). 

> Внимание! Все ресурсы `LVMVolumeGroup`, указанные в `Spec` ресурса `DRBDStoragePool`, должны быть на разных узлах. (Запрещено указывать несколько ресурсов `LVMVolumeGroup`, которые расположены на одном и том же узле.) 

Результатом обработки пользовательского ресурса `DRBDStoragePool` станет создание необходимого `Storage Pool` в `LINSTOR`.

> Имя созданного `Storage Pool` будет соответствовать имени созданного ресурса `DRBDStoragePool`.
> Узлы, на которых будет создан `Storage Pool`, будут взяты из ресурсов LVMVolumeGroup.

Информацию о ходе выполнения работы контроллера и ее результатах можно посмотреть в поле `Status` созданного ресурса `DRBDStoragePool`.

> Перед фактической работой с `LINSTOR` контроллер провалидирует предоставленную ему конфигурацию и в случае ошибки предоставит информацию о причинах неудачи.
> Невалидные `Storage Pool'ы` не будут созданы в `LINSTOR`.
> 
> В случае возникновения ошибки при создании `Storage Pool` в `LINSTOR` контроллер удалит за собой невалидный `Storage Pool` в `LINSTOR`.

#### Обновление `DRBDStoragePool`-ресурса
Пользователь имеет возможность обновить существующую конфигурацию `Storage Pool` в `LINSTOR`. Для этого пользователю необходимо внести желаемые изменения в поле `Spec` соответствующего ресурса.

После внесения изменений в ресурс `sds-drbd-controller` провалидирует новую конфигурацию и в случае валидных данных выполнит необходимые операции по обновлению `Storage Pool` в `LINSTOR`. Результаты данной операции также будут отображены в поле `Status` ресурса `DRBDStoragePool`.

> Обратите внимание, что поле `Spec.Type` ресурса `DRBDStoragePool` **неизменяемое**. 
> 
> Контроллер не реагирует на внесенные пользователем изменения в поле `Status` ресурса.

#### Удаление `DRBDStoragePool`-ресурса
В настоящий момент `sds-drbd-controller` никак не обрабатывает удаление ресурсов `DRBDStoragePool`.

> Удаление ресурса никаким образом не затрагивает созданные по нему `Storage Pool` в `LINSTOR`. Если пользователь воссоздаст удаленный ресурс с тем же именем и конфигурацией, контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `Status.Phase` созданного ресурса будет отображено значение `Created`.

### Работа с ресурсами [DRBDStorageClass]()

#### Создание ресурса `DRBDStorageClass`
`DRBDStorageClass`-ресурс позволяет создать необходимый `StorageClass` в `Kubernetes` для выбранного `Storage Pool` в `LINSTOR`.
Для этого пользователю необходимо вручную создать ресурс и заполнить поле `Spec` (то есть желаемое состояние `StorageClass` в `Kubernetes`).

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