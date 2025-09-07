---
title: "Модуль sds-replicated-volume"
description: "Модуль sds-replicated-volume: общие концепции и положения."
moduleStatus: preview
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [системных требований](./readme.html#системные-требования-и-рекомендации).
Использование в других условиях возможно, но стабильная работа в таких случаях не гарантируется.
{{< /alert >}}

Модуль управляет реплицируемым блочным хранилищем на базе `DRBD`. В качестве control-plane/бэкенда используется `LINSTOR` (без возможности непосредственной настройки пользователем).

Модуль позволяет создавать `Storage Pool` и `StorageClass` через создание [пользовательских ресурсов Kubernetes](./cr.html).

Для создания `Storage Pool` потребуются настроенные на узлах кластера `LVMVolumeGroup`. Настройка `LVM` осуществляется модулем [sds-node-configurator](../../sds-node-configurator/stable/).

> **Внимание.** Перед включением модуля `sds-replicated-volume` необходимо включить модуль `sds-node-configurator`.
>
> **Внимание.** Синхронизация данных при репликации томов происходит только в синхронном режиме, асинхронный режим не поддерживается.
>
> **Внимание.** Если в кластере используется только одна нода, то вместо `sds-replicated-volume` рекомендуется использовать `sds-local-volume`.
> Для использования `sds-replicated-volume` необходимо иметь минимально 3 ноды. Рекомендуется использовать 4 и более на случай выхода нод из строя.

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse, останется только создать [ReplicatedStoragePool и ReplicatedStorageClass](./usage.html#конфигурация-бэкенда-linstor).

Для корректной работы модуля `sds-replicated-volume` выполните следующие шаги:

- Включите модуль [sds-node-configurator](../../sds-node-configurator/stable/).

  Убедитесь, что модуль `sds-node-configurator` включен **до** включения модуля `sds-replicated-volume`.

{{< alert level="warning" >}}
Непосредственная конфигурация бэкенда LINSTOR пользователем запрещена.
{{< /alert >}}

{{< alert level="info" >}}
Синхронизация данных при репликации томов происходит только в синхронном режиме, асинхронный режим не поддерживается.
{{< /alert >}}

{{< alert level="info" >}}
Для работы с снапшотами требуется подключенный модуль [snapshot-controller](../../snapshot-controller/).
{{< /alert >}}

- Настройте LVMVolumeGroup.
  
  Перед созданием StorageClass необходимо создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) модуля `sds-node-configurator` на узлах кластера.

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

2. Дождитесь, пока модуль `sds-node-configurator` перейдёт в состояние `Ready`:

   ```shell
   kubectl get module sds-node-configurator -w
   ```

3. Активируйте модуль `sds-replicated-volume`. Перед включением рекомендуется ознакомиться [с доступными настройками](./configuration.html).

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

4. Дождитесь пока модуль `sds-replicated-volume` перейдёт в состояние `Ready`:

   ```shell
   kubectl get module sds-replicated-volume -w
   ```

5. Убедитесь, что в пространствах имен `d8-sds-replicated-volume` и `d8-sds-node-configurator` все поды находятся в статусе `Running` или `Completed` и запущены на всех узлах, где планируется использовать ресурсы DRBD.

   ```shell
   kubectl -n d8-sds-replicated-volume get pod -o wide -w
   kubectl -n d8-sds-node-configurator get pod -o wide -w
   ```

### Настройка хранилища на узлах

Необходимо на этих узлах создать группы томов `LVM` с помощью пользовательских ресурсов `LVMVolumeGroup`. В быстром старте будем создавать обычное `Thick` хранилище. Подробнее про пользовательские ресурсы и примеры их использования можно прочитать в [примерах использования](./usage.html).

Приступим к настройке хранилища:

- Получить все ресурсы [BlockDevice](../../sds-node-configurator/stable/cr.html#blockdevice), которые доступны в вашем кластере:

```shell
kubectl get bd

NAME                                           NODE       CONSUMABLE   SIZE      PATH
dev-0a29d20f9640f3098934bca7325f3080d9b6ef74   worker-0   true         30Gi      /dev/vdd
dev-457ab28d75c6e9c0dfd50febaac785c838f9bf97   worker-0   false        20Gi      /dev/vde
dev-49ff548dfacba65d951d2886c6ffc25d345bb548   worker-1   true         35Gi      /dev/vde
dev-75d455a9c59858cf2b571d196ffd9883f1349d2e   worker-2   true         35Gi      /dev/vdd
dev-ecf886f85638ee6af563e5f848d2878abae1dcfd   worker-0   true         5Gi       /dev/vdb
```

- Создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-0`:

```yaml
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LVMVolumeGroup
metadata:
  name: "vg-1-on-worker-0" # Имя может быть любым подходящим для имен ресурсов в Kubernetes. Именно это имя ресурса LVMVolumeGroup будет в дальнейшем использоваться для создания ReplicatedStoragePool
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
  actualVGNameOnTheNode: "vg-1" # имя LVM VG, которая будет создана на узле из указанных выше блочных устройств
EOF
```

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Ready`:

```shell
kubectl get lvg vg-1-on-worker-0 -w
```

- Если ресурс перешел в состояние `Ready`, то это значит, что на узле `worker-0` из блочных устройств `/dev/vdd` и `/dev/vdb` была создана LVM VG с именем `vg-1`.

- Далее создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-1`:

```shell
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

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Ready`:

```shell
kubectl get lvg vg-1-on-worker-1 -w
```

- Если ресурс перешел в состояние `Ready`, то это значит, что на узле `worker-1` из блочного устройства `/dev/vde` была создана LVM VG с именем `vg-1`.

- Далее создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-2`:

```shell
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

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Ready`:

```shell
kubectl get lvg vg-1-on-worker-2 -w
```

- Если ресурс перешел в состояние `Ready`, то это значит, что на узле `worker-2` из блочного устройства `/dev/vdd` была создана LVM VG с именем `vg-1`.

- Теперь, когда у нас на узлах созданы нужные LVM VG, мы можем создать из них [ReplicatedStoragePool](./cr.html#replicatedstoragepool):

```yaml
kubectl apply -f -<<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStoragePool
metadata:
  name: data
spec:
  type: LVM
  lvmVolumeGroups: # Здесь указываем имена ресурсов LVMVolumeGroup, которые мы создавали ранее
    - name: vg-1-on-worker-0
    - name: vg-1-on-worker-1
    - name: vg-1-on-worker-2
EOF

```

- Дождаться, когда созданный ресурс `ReplicatedStoragePool` перейдет в состояние `Completed`:

```shell
kubectl get rsp data -w
```

- Проверить, что Storage Pool `data` создался на узлах `worker-0`,  `worker-1` и `worker-2`:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor sp l

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

- Создать ресурс [ReplicatedStorageClass](./cr.html#replicatedstorageclass) для кластера, в котором нет зон (работа зональных ReplicatedStorageClass подробнее описана в [сценариях использования](./layouts.html)):

```yaml
kubectl apply -f -<<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: replicated-storage-class
spec:
  storagePool: data # Указываем имя ReplicatedStoragePool, созданного ранее
  reclaimPolicy: Delete
  topology: Ignored # - если указываем такую топологию, то в кластере не должно быть зон (узлов с метками topology.kubernetes.io/zone).
EOF
```

- Дождаться, когда созданный ресурс `ReplicatedStorageClass` перейдет в состояние `Created`:

```shell
kubectl get rsc replicated-storage-class -w
```

- Проверить, что соответствующий `StorageClass` создался:

```shell
kubectl get sc replicated-storage-class
```

- Если `StorageClass` с именем `replicated-storage-class` появился, значит настройка модуля `sds-replicated-volume` завершена. Теперь пользователи могут создавать PV, указывая `StorageClass` с именем `replicated-storage-class`. При указанных выше настройках будет создаваться том с 3мя репликами на разных узлах.

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
