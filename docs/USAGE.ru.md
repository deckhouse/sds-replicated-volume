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

**Внимание.** Поле [`spec.type`](./cr.html#replicatedstoragepool-v1alpha1-spec-type) ресурса [ReplicatedStoragePool](./cr.html#replicatedstoragepool) **неизменяемое**. Контроллер не реагирует на изменения в поле [`status`](./cr.html#replicatedstoragepool-v1alpha1-status) ресурса.

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

> Перед процессом создания StorageClass запустится процесс валидации предоставленной конфигурации.
> В случае обнаружения ошибок StorageClass создан не будет, а в поле `status` ресурса ReplicatedStorageClass отобразится информация об ошибке.

Результатом обработки ресурса ReplicatedStorageClass станет создание необходимого StorageClass в Kubernetes.

{{< alert level="warning" >}}
Все поля в `spec` ресурса ReplicatedStorageClass являются **неизменяемыми**.
{{< /alert >}}

Поле `status` будет обновляться контроллером `sds-replicated-volume-controller` для отображения информации о результатах проводимых операций.

#### Обновление ресурса ReplicatedStorageClass

Поменять параметры StorageClass, созданного через ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass), на данный момент **невозможно**.

#### Удаление ресурса ReplicatedStorageClass

1. Удалите ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass), чтобы удалить связанный StorageClass в Kubernetes.

1. Дождитесь, пока `sds-replicated-volume-controller` обнаружит удаление и выполнит все необходимые операции для корректного удаления дочернего StorageClass.

> `sds-replicated-volume-controller` выполнит удаление дочернего StorageClass только в случае, если в поле `status.phase` ресурса ReplicatedStorageClass будет указано значение `Created`. В иных случаях будет удалён только ресурс ReplicatedStorageClass, а дочерний StorageClass затронут не будет.

## Дополнительные возможности для приложений

### Размещение приложения «поближе» к данным (data locality)

В случае гиперконвергентной инфраструктуры может возникнуть задача по приоритетному размещению пода приложения на узлах, где необходимые ему данные хранилища расположены локально. Это позволит получить максимальную производительность хранилища.

Для решения этой задачи модуль предоставляет специальный планировщик, который учитывает размещение данных в хранилище и старается размещать под в первую очередь на тех узлах, где данные доступны локально. Данный планировщик назначается автоматически для любого пода, использующего тома `sds-replicated-volume`.

Data locality настраивается параметром [`volumeAccess`](./cr.html#replicatedstorageclass-v1alpha1-spec-volumeaccess) при создании ресурса [ReplicatedStorageClass](./cr.html#replicatedstorageclass).
