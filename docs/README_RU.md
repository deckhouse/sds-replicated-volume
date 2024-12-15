---
title: "Модуль sds-replicated-volume"
description: "Модуль sds-replicated-volume: общие концепции и положения."
moduleStatus: preview
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [системных требований](./readme.html#системные-требования-и-рекомендации).
Использование в других условиях возможно, но стабильная работа в таких случаях не гарантируется.
{{< /alert >}}

Модуль управляет реплицируемым блочным хранилищем на основе DRBD с использованием LINSTOR в качестве управляющего слоя (control plane). Позволяет создавать Storage Pool в LINSTOR и StorageClass в Kubernetes через создание [пользовательских ресурсов Kubernetes](./cr.html).

## Шаги настройки модуля

Для корректной работы модуля `sds-replicated-volume` выполните следующие шаги:

- Настройте LVMVolumeGroup.
  
  Перед созданием StorageClass необходимо создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) модуля `sds-node-configurator` на узлах кластера.

- Включите модуль [sds-node-configurator](../../sds-node-configurator/stable/).

  Убедитесь, что модуль `sds-node-configurator` включен **до** включения модуля `sds-replicated-volume`.

{{< alert level="warning" >}}
Непосредственная конфигурация бэкенда LINSTOR пользователем запрещена.
{{< /alert >}}

{{< alert level="info" >}}
Синхронизация данных при репликации томов происходит только в синхронном режиме, асинхронный режим не поддерживается.
{{< /alert >}}

- Создайте пулы хранения и соответствующие StorageClass'ы.

  Создание StorageClass для CSI-драйвера `replicated.csi.storage.deckhouse.io` пользователем **запрещено**.
  
  После активации модуля `sds-replicated-volume` в конфигурации Deckhouse кластер автоматически настроится на работу с бэкендом LINSTOR. Вам останется лишь выполнить создание [пулов хранения и StorageClass'ов](./usage.html#конфигурация-бэкенда-linstor).

Модуль поддерживает два режима работы: LVM и LVMThin.
У каждого из них есть свои особенности, преимущества и ограничения. Подробнее о различиях можно узнать [в FAQ](./faq.html#когда-следует-использовать-lvm-а-когда-lvmthin).

## Быстрый старт

Все команды выполняются на машине с доступом к API Kubernetes и правами администратора.

### Включение модулей

Включение модуля `sds-node-configurator`:

1. Создайте ресурс ModuleConfig для включения модуля:

   ```yaml
   kubectl apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-node-configurator
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Дождитесь состояния модуля `Ready`. На этом этапе не требуется проверять поды в пространстве имен `d8-sds-node-configurator`.

  ```shell
  kubectl get module sds-node-configurator -w
  ```

Включение модуля `sds-replicated-volume`:

1. Активируйте модуль `sds-replicated-volume`. Перед включением рекомендуется ознакомиться [с доступными настройками](./configuration.html).
Пример ниже запускает модуль с настройками по умолчанию, что приведет к созданию служебных подов компонента `sds-replicated-volume` на всех узлах кластера, установит модуль ядра DRBD и зарегестрирует CSI драйвер:

   ```yaml
   kubectl apply -f - <<EOF
   apiVersion: deckhouse.io/v1alpha1
   kind: ModuleConfig
   metadata:
     name: sds-replicated-volume
   spec:
     enabled: true
     version: 1
   EOF
   ```

1. Дождитесь состояния модуля `Ready`.

   ```shell
   kubectl get module sds-replicated-volume -w
   ```

1. Убедитесь, что в пространствах имен `d8-sds-replicated-volume` и `d8-sds-node-configurator` все поды находятся в статусе `Running` или `Completed` и запущены на всех узлах, где планируется использовать ресурсы DRBD.

   ```shell
   kubectl -n d8-sds-replicated-volume get pod -owide -w
   kubectl -n d8-sds-node-configurator get pod -o wide -w
   ```

### Настройка хранилища на узлах

Для настройки хранилища на узлах необходимо создать группы томов LVM с использованием ресурсов LVMVolumeGroup. В данном примере создается хранилище Thick. Подробнее про пользовательские ресурсы и примеры их использования можно прочитать [в примерах использования](./usage.html).

#### Шаги настройки

1. Получите все ресурсы [BlockDevice](../../sds-node-configurator/stable/cr.html#blockdevice), которые доступны в вашем кластере:

   ```shell
   kubectl get bd
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

1. Создайте ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-0`:

   ```yaml
   kubectl apply -f - <<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: LVMVolumeGroup
   metadata:
     name: "vg-1-on-worker-0" # Имя может быть любым подходящим для имен ресурсов в Kubernetes. Именно это имя ресурса LVMVolumeGroup будет в дальнейшем использоваться для создания ReplicatedStoragePool.
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
     actualVGNameOnTheNode: "vg-1" # Имя LVM VG, которая будет создана на узле из указанных выше блочных устройств.
   EOF
   ```

1. Дождитесь, когда созданный ресурс LVMVolumeGroup перейдет в состояние `Ready`:

   ```shell
   kubectl get lvg vg-1-on-worker-0 -w
   ```

Если ресурс перешел в состояние `Ready`, это значит, что на узле `worker-0` из блочных устройств `/dev/vdd` и `/dev/vdb` была создана LVM VG с именем `vg-1`.

1. Создайте ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-1`:

   ```yaml
   kubectl apply -f - <<EOF
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

1. Дождитесь, когда созданный ресурс LVMVolumeGroup перейдет в состояние `Ready`:

   ```shell
   kubectl get lvg vg-1-on-worker-1 -w
   ```

Если ресурс перешел в состояние `Ready`, это значит, что на узле `worker-1` из блочного устройства `/dev/vde` была создана LVM VG с именем `vg-1`.

1. Создайте ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-2`:

   ```yaml
   kubectl apply -f - <<EOF
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

1. Дождитесь, когда созданный ресурс LVMVolumeGroup перейдет в состояние `Ready`:

   ```shell
   kubectl get lvg vg-1-on-worker-2 -w
   ```

Если ресурс перешел в состояние `Ready`, это значит, что на узле `worker-2` из блочного устройства `/dev/vdd` была создана LVM VG с именем `vg-1`.

1. Создайте ресурс [ReplicatedStoragePool](./cr.html#replicatedstoragepool):

   ```yaml
   kubectl apply -f -<<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStoragePool
   metadata:
     name: data
   spec:
     type: LVM
     lvmVolumeGroups: # Здесь указываем имена ресурсов LVMVolumeGroup, которые мы создавали ранее.
       - name: vg-1-on-worker-0
       - name: vg-1-on-worker-1
       - name: vg-1-on-worker-2
   EOF
   ```

1. Дождитесь, когда созданный ресурс `ReplicatedStoragePool` перейдет в состояние `Created`:

   ```shell
   kubectl get rsp data -w
   ```

1. Проверьте, что в LINSTOR создался Storage Pool `data` на узлах `worker-0`,  `worker-1` и `worker-2`:

   ```shell
   alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
   linstor sp l
   ```

   Пример вывода:

   ```shell
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

1. Создайте ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) для кластера, не разделенного на зоны (подробности о работе зональных ReplicatedStorageClass представлены в разделе [сценарии использования](./layouts.html)):

   ```yaml
   kubectl apply -f -<<EOF
   apiVersion: storage.deckhouse.io/v1alpha1
   kind: ReplicatedStorageClass
   metadata:
     name: replicated-storage-class
   spec:
     storagePool: data # Имя ReplicatedStoragePool, созданного ранее.
     reclaimPolicy: Delete
     topology: Ignored # Если указана такая топология, то в кластере не должно быть зон (узлов с метками topology.kubernetes.io/zone).
   EOF
   ```

1. Дождитесь, когда созданный ресурс `ReplicatedStorageClass` перейдет в состояние `Created`:

   ```shell
   kubectl get rsc replicated-storage-class -w
   ```

1. Проверьте, что соответствующий StorageClass создался:

   ```shell
   kubectl get sc replicated-storage-class
   ```

Если StorageClass с именем `replicated-storage-class` появился, значит настройка модуля `sds-replicated-volume` завершена. Теперь пользователи могут создавать PVC, указывая StorageClass с именем `replicated-storage-class`. С данными настройками для каждого тома будут создаваться три реплики, распределенные по различным узлам.

## Системные требования и рекомендации

### Требования

{{< alert level="info" >}}
Применительно как к однозональным кластерам, так и к кластерам с использованием нескольких зон доступности.
{{< /alert >}}

- Используйте стоковые ядра, поставляемые вместе [с поддерживаемыми дистрибутивами](https://deckhouse.ru/documentation/v1/supported_versions.html#linux).
- Для сетевого соединения необходимо использовать инфраструктуру с пропускной способностью 10 Gbps или выше.
- Чтобы достичь максимальной производительности, сетевая задержка между узлами должна находиться в пределах 0,5–1 мс.
- Не используйте другой SDS (Software defined storage) для предоставления дисков SDS Deckhouse.

### Рекомендации

- Не используйте RAID. Подробнее [в FAQ](./faq.html#почему-не-рекомендуется-использовать-raid-для-дисков-которые-используются-модулем-sds-replicated-volume).
- Используйте локальные физические диски. Подробнее [в FAQ](./faq.html#почему-вы-рекомендуете-использовать-локальные-диски-не-nas).
- Для стабильной работы кластера, но с ухудшением производительности, допустимая сетевая задержка между узлами не должна превышать 20 мс.
