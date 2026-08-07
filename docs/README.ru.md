---
title: "Модуль sds-replicated-volume"
description: "Модуль sds-replicated-volume: общие концепции и положения."
moduleStatus: preview
---

Модуль `sds-replicated-volume` управляет реплицируемым блочным хранилищем на базе `DRBD`. В качестве control plane используется `LINSTOR`. Storage Pool и StorageClass задаются через [пользовательские ресурсы Kubernetes](./cr.html), непосредственная настройка бэкенда `LINSTOR` не поддерживается.

После включения модуля создайте [ReplicatedStoragePool](./usage.html#создание-ресурса-replicatedstoragepool) и [ReplicatedStorageClass](./usage.html#создание-ресурса-replicatedstorageclass).

## Системные требования и рекомендации

Работоспособность модуля гарантируется только при соблюдении требований ниже. Использование в других условиях возможно, но стабильная работа не гарантируется.

### Требования

Кластер должен соответствовать следующим требованиям (для однозональных и многозональных кластеров):

- Перед включением `sds-replicated-volume` включите модуль [`sds-node-configurator`](/modules/sds-node-configurator/). Storage Pool строится на ресурсах LVMVolumeGroup, которые настраивает этот модуль.
- Подключите модуль [`snapshot-controller`](/modules/snapshot-controller/).
- Используйте минимум 3 узла. Рекомендуется 4 и более на случай выхода узлов из строя. Если в кластере один узел, используйте [`sds-local-volume`](/modules/sds-local-volume/) вместо `sds-replicated-volume`.
- Не настраивайте бэкенд LINSTOR напрямую.
- Не создавайте вручную StorageClass для CSI-драйвера `replicated.csi.storage.deckhouse.io`.
- Поддерживаемые режимы доступа: `RWO` и `RWX` (только в DVP).
- Репликация выполняется только синхронно; асинхронный режим не поддерживается.
- Поддерживаемые режимы хранения: `LVM` и `LVMThin`. Подробнее о различиях — [в FAQ](./faq.html#когда-следует-использовать-lvm-а-когда-lvmthin).
- Используйте стоковые ядра, поставляемые вместе [с поддерживаемыми дистрибутивами](/products/kubernetes-platform/documentation/v1/reference/supported_versions.html).
- Для сетевого соединения используйте инфраструктуру с пропускной способностью 10 Gbps или выше.
- Чтобы достичь максимальной производительности, сетевая задержка между узлами должна находиться в пределах 0,5–1 мс. При задержках более 5 мс будут возникать серьёзные проблемы с производительностью.
- Не используйте другой Software Defined Storage (SDS) для предоставления дисков модулю `sds-replicated-volume`.
- Чтобы работала репликация DRBD, разрешите взаимодействие между узлами по портам `7000`–`7999` по протоколу UDP. Подробнее — в таблице [«Трафик между узлами»](/products/kubernetes-platform/documentation/v1/reference/network_interaction.html#трафик-между-узлами). При необходимости переопределите диапазон портов с помощью [настройки `drbdPortRange`](./configuration.html#parameters-drbdportrange), указав `minPort` и `maxPort`.

  После изменения параметров `drbdPortRange` перезапустите контроллер LINSTOR, чтобы новые настройки вступили в силу. Существующие DRBD-ресурсы сохранят назначенные им порты.

### Рекомендации

При планировании хранилища соблюдайте следующие рекомендации:

- Не используйте RAID. Подробнее — [в FAQ](./faq.html#почему-не-рекомендуется-использовать-raid-для-дисков-которые-используются-модулем-sds-replicated-volume).
- Используйте локальные физические диски. Подробнее — [в FAQ](./faq.html#почему-вы-рекомендуете-использовать-локальные-диски-не-nas).
- При ухудшении производительности сети для сохранения стабильной работы кластера задержка между узлами не должна превышать 10 мс.
- Для гарантированной консистентности данных используйте [ReplicatedStorageClass](./cr.html#replicatedstorageclass) с режимом репликации `ConsistencyAndAvailability` ([`spec.replication`](./cr.html#replicatedstorageclass-v1alpha1-spec-replication)) — этот режим используется по умолчанию.

{{< alert level="warning" >}}
Изменение режима на `Availability` может привести к split brain и потере данных при проблемах с сетевой связностью.
{{< /alert >}}

## Быстрый старт

Выполняйте все команды на машине с доступом к API Kubernetes и правами администратора.

### Включение модулей

1. Создайте ресурс ModuleConfig для включения модуля [`sds-node-configurator`](/modules/sds-node-configurator/):

   ```shell
   d8 k apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-node-configurator
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Дождитесь, пока модуль `sds-node-configurator` перейдёт в состояние `Ready`:

   ```shell
   d8 k get module sds-node-configurator -w
   ```

1. Включите модуль `sds-replicated-volume`. Перед включением ознакомьтесь с [доступными настройками](./configuration.html).

   Пример ниже запускает модуль с настройками по умолчанию: служебные поды создаются на всех узлах кластера, устанавливается модуль ядра DRBD, регистрируется CSI-драйвер:

   ```shell
   d8 k apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-replicated-volume
   spec:
     enabled: true
     version: 2
   EOF
   ```

1. Дождитесь, пока модуль `sds-replicated-volume` перейдёт в состояние `Ready`:

   ```shell
   d8 k get module sds-replicated-volume -w
   ```

1. Убедитесь, что в неймспейсах `d8-sds-replicated-volume` и `d8-sds-node-configurator` все поды находятся в статусе `Running` или `Completed` и запущены на всех узлах, где планируется использовать ресурсы DRBD:

   ```shell
   d8 k -n d8-sds-replicated-volume get pod -o wide -w
   d8 k -n d8-sds-node-configurator get pod -o wide -w
   ```

### Выбор узлов для данных

Параметр [`settings.dataNodes.nodeSelector`](./configuration.html#parameters-datanodes-nodeselector) рекомендуется указывать при включении модуля.

Уже добавленные лейблы `storage.deckhouse.io/sds-replicated-volume-*` не удаляются автоматически: в текущей версии control plane нет механизма автоматической эвакуации данных с узлов.

Чтобы убрать ресурсы модуля с узла, не удаляя сам узел из кластера:

1. На любом master-узле запустите [скрипт эвакуации](./faq.html#%D0%BF%D1%80%D0%B8%D0%BC%D0%B5%D1%80-%D1%83%D0%B4%D0%B0%D0%BB%D0%B5%D0%BD%D0%B8%D1%8F-%D1%80%D0%B5%D1%81%D1%83%D1%80%D1%81%D0%BE%D0%B2-%D1%81-%D1%83%D0%B7%D0%BB%D0%B0-%D0%B1%D0%B5%D0%B7-%D1%83%D0%B4%D0%B0%D0%BB%D0%B5%D0%BD%D0%B8%D1%8F-%D1%81%D0%B0%D0%BC%D0%BE%D0%B3%D0%BE-%D1%83%D0%B7%D0%BB%D0%B0) `/opt/deckhouse/sbin/evict.sh` с параметром `--delete-resources-only`.
1. После эвакуации удалите с узла лейблы модуля и удалите узел из LINSTOR:

   ```shell
   export NODE_NAME=<NODE_NAME>
   d8 k get node $NODE_NAME -o jsonpath='{.metadata.labels}' | jq -r 'keys[] | select(startswith("storage.deckhouse.io/sds-replicated-volume-"))' | while read label; do
     d8 k label node $NODE_NAME "$label"-
   done
   d8 k -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor node lost $NODE_NAME
   ```

   где `<NODE_NAME>` — имя узла Kubernetes.

### Настройка хранилища на узлах

Создайте группы томов LVM с помощью ресурсов [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup). В быстром старте создаётся Thick-хранилище. Подробнее — в [примерах использования](./usage.html).

1. Получите все ресурсы [BlockDevice](/modules/sds-node-configurator/cr.html#blockdevice), доступные в кластере:

   ```shell
   d8 k get bd
   ```

   Пример вывода:

   ```console
   NAME                                           NODE       CONSUMABLE   SIZE      PATH
   dev-0a29d20f9640f3098934bca7325f3080d9b6ef74   worker-0   true         30Gi      /dev/vdd
   dev-457ab28d75c6e9c0dfd50febaac785c838f9bf97   worker-0   false        20Gi      /dev/vde
   dev-49ff548dfacba65d951d2886c6ffc25d345bb548   worker-1   true         35Gi      /dev/vde
   dev-75d455a9c59858cf2b571d196ffd9883f1349d2e   worker-2   true         35Gi      /dev/vdd
   dev-ecf886f85638ee6af563e5f848d2878abae1dcfd   worker-0   true         5Gi       /dev/vdb
   ```

1. Создайте ресурс [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) для узла `worker-0`:

   ```shell
   d8 k apply -f - <<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: LVMVolumeGroup
   metadata:
     # Используйте любое подходящее имя для ресурсов в Kubernetes. Это имя ресурса LVMVolumeGroup будет в дальнейшем использоваться для создания ReplicatedStoragePool.
     name: "vg-1-on-worker-0"
   spec:
     type: Local
     local:
       nodeName: "worker-0"
     blockDeviceSelector:
       matchExpressions:
         - key: kubernetes.io/metadata.name
           operator: In
           values:
             - dev-0a29d20f9640f3098934bca7325f3080d9b6ef74
             - dev-ecf886f85638ee6af563e5f848d2878abae1dcfd
     # Имя LVM VG, которая будет создана на узле из указанных выше блочных устройств.
     actualVGNameOnTheNode: "vg-1"
   EOF
   ```

1. Дождитесь, когда ресурс LVMVolumeGroup перейдёт в состояние `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-0 -w
   ```

   Если ресурс в состоянии `Ready`, на узле `worker-0` из устройств `/dev/vdd` и `/dev/vdb` создана LVM VG с именем `vg-1`.

1. Создайте ресурс [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) для узла `worker-1`:

   ```shell
   d8 k apply -f - <<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: LVMVolumeGroup
   metadata:
     name: "vg-1-on-worker-1"
   spec:
     type: Local
     local:
       nodeName: "worker-1"
     blockDeviceSelector:
       matchExpressions:
         - key: kubernetes.io/metadata.name
           operator: In
           values:
             - dev-49ff548dfacba65d951d2886c6ffc25d345bb548
     actualVGNameOnTheNode: "vg-1"
   EOF
   ```

1. Дождитесь, когда ресурс LVMVolumeGroup перейдёт в состояние `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-1 -w
   ```

   Если ресурс в состоянии `Ready`, на узле `worker-1` из устройства `/dev/vde` создана LVM VG с именем `vg-1`.

1. Создайте ресурс [LVMVolumeGroup](/modules/sds-node-configurator/cr.html#lvmvolumegroup) для узла `worker-2`:

   ```shell
   d8 k apply -f - <<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: LVMVolumeGroup
   metadata:
     name: "vg-1-on-worker-2"
   spec:
     type: Local
     local:
       nodeName: "worker-2"
     blockDeviceSelector:
       matchExpressions:
         - key: kubernetes.io/metadata.name
           operator: In
           values:
             - dev-75d455a9c59858cf2b571d196ffd9883f1349d2e
     actualVGNameOnTheNode: "vg-1"
   EOF
   ```

1. Дождитесь, когда ресурс LVMVolumeGroup перейдёт в состояние `Ready`:

   ```shell
   d8 k get lvg vg-1-on-worker-2 -w
   ```

   Если ресурс в состоянии `Ready`, на узле `worker-2` из устройства `/dev/vdd` создана LVM VG с именем `vg-1`.

1. Создайте [ReplicatedStoragePool](./cr.html#replicatedstoragepool) из созданных LVM VG:

   ```shell
   d8 k apply -f -<<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: data
   spec:
     type: LVM
     # Укажите здесь имена ресурсов LVMVolumeGroup, которые вы создали ранее.
     lvmVolumeGroups:
       - name: vg-1-on-worker-0
       - name: vg-1-on-worker-1
       - name: vg-1-on-worker-2
   EOF
   ```

1. Дождитесь, когда ресурс ReplicatedStoragePool перейдёт в состояние `Completed`:

   ```shell
   d8 k get rsp data -w
   ```

1. Проверьте, что Storage Pool `data` создан на узлах `worker-0`, `worker-1` и `worker-2`:

   ```shell
   alias linstor='d8 k -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor sp l
   ```

   Пример вывода:

   ```console
   ╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
   ┊ StoragePool          ┊ Node     ┊ Driver   ┊ PoolName ┊ FreeCapacity ┊ TotalCapacity ┊ CanSnapshots ┊ State ┊ SharedName                    ┊
   ╞═════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╡
   ┊ DfltDisklessStorPool ┊ worker-0 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-0;DfltDisklessStorPool ┊
   ┊ DfltDisklessStorPool ┊ worker-1 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-1;DfltDisklessStorPool ┊
   ┊ DfltDisklessStorPool ┊ worker-2 ┊ DISKLESS ┊          ┊              ┊               ┊ False        ┊ Ok    ┊ worker-2;DfltDisklessStorPool ┊
   ┊ data                 ┊ worker-0 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-0;data                 ┊
   ┊ data                 ┊ worker-1 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-1;data                 ┊
   ┊ data                 ┊ worker-2 ┊ LVM      ┊ vg-1     ┊    35.00 GiB ┊     35.00 GiB ┊ False        ┊ Ok    ┊ worker-2;data                 ┊
   ╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
   ```

1. Создайте ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) для кластера без зон (зональные сценарии — в [сценариях использования](./layouts.html)):

   ```shell
   d8 k apply -f -<<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: replicated-storage-class
   spec:
     # Укажите имя созданного ранее ресурса ReplicatedStoragePool.
     storagePool: data
     reclaimPolicy: Delete
     # При такой топологии в кластере не должно быть зон (узлов с лейблами topology.kubernetes.io/zone).
     topology: Ignored
   EOF
   ```

1. Дождитесь, когда ресурс ReplicatedStorageClass перейдёт в состояние `Created`:

   ```shell
   d8 k get rsc replicated-storage-class -w
   ```

1. Проверьте, что соответствующий StorageClass создан:

   ```shell
   d8 k get sc replicated-storage-class
   ```

   Если StorageClass с именем `replicated-storage-class` появился, настройка модуля завершена. Пользователи могут создавать PV, указывая этот StorageClass. При указанных настройках создаётся том с тремя репликами на разных узлах.
