---
title: "Модуль sds-replicated-volume"
description: "Модуль sds-replicated-volume: общие концепции и положения."
moduleStatus: experimental
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [требований](./readme.html#системные-требования-и-рекомендации).
Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

Модуль управляет реплицируемым блочным хранилищем на базе `DRBD`. На текущий момент в качестве control-plane используется `LINSTOR`. Модуль позволяет создавать `Storage Pool` в `LINSTOR` и `StorageClass` в `Kubernetes` через создание [пользовательских ресурсов Kubernetes](./cr.html).
Для создания `Storage Pool` потребуются настроенные на узлах кластера `LVMVolumeGroup`. Настройка `LVM` осуществляется модулем [sds-node-configurator](../../sds-node-configurator/).
> **Внимание!** Перед включением модуля `sds-replicated-volume` необходимо включить модуль `sds-node-configurator`.
>
> **Внимание!** Непосредственная конфигурация бэкенда `LINSTOR` пользователем запрещена.
>
> **Внимание!** Синхронизация данных при репликации томов происходит только в синхронном режиме, асинхронный режим не поддерживается.

После включения модуля `sds-replicated-volume` в конфигурации Deckhouse ваш кластер будет автоматически настроен на использование бэкенда `LINSTOR`. Останется только создать [пулы хранения и StorageClass'ы](./usage.html#конфигурация-бэкенда-linstor).

> **Внимание!** Создание `StorageClass` для CSI-драйвера replicated.csi.storage.deckhouse.io пользователем запрещено.

Поддерживаются два режима — LVM и LVMThin.
Каждый из них имеет свои достоинства и недостатки, подробнее о различиях читайте в [FAQ](./faq.html#когда-следует-использовать-lvm-а-когда-lvmthin).

## Быстрый старт

Все команды следует выполнять на машине, имеющей доступ к API Kubernetes с правами администратора.

### Включение модулей

- Включить модуль sds-node-configurator

```shell
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

- Дождаться, когда модуль перейдет в состояние `Ready`. На этом этапе НЕ нужно проверять поды в namespace `d8-sds-node-configurator`.

```shell
kubectl get mc sds-node-configurator -w
```

- Включить модуль `sds-replicated-volume`. Возможные настройки модуля рекомендуем посмотреть в [конфигурации](./configuration.html). В примере ниже модуль запускается с настройками по умолчанию. Это приведет к тому, что на всех узлах кластера будет:
  - установлен модуль ядра `DRBD`;
  - зарегистрирован CSI драйвер;
  - запущены служебные поды компонентов `sds-replicated-volume`.

```shell
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

- Дождаться, когда модуль перейдет в состояние `Ready`.

```shell
kubectl get mc sds-replicated-volume -w
```

- Проверить, что в namespace `d8-sds-replicated-volume` и `d8-sds-node-configurator` все поды в состоянии `Running` или `Completed` и запущены на всех узлах, где планируется использовать ресурсы `DRBD`.

```shell
kubectl -n d8-sds-replicated-volume get pod -owide -w
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
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-0" # Имя может быть любым подходящим для имен ресурсов в Kubernetes. Именно это имя ресурса LvmVolumeGroup будет в дальнейшем использоваться для создания ReplicatedStoragePool
spec:
  type: Local
  blockDeviceNames:  # указываем имена ресурсов BlockDevice, которые расположены на нужной нам узле и CONSUMABLE которых выставлен в true. Обратите внимание, что имя узлы мы ннигде не указываем. Имя узлы берется из ресурсов BlockDevice
    - dev-0a29d20f9640f3098934bca7325f3080d9b6ef74
    - dev-ecf886f85638ee6af563e5f848d2878abae1dcfd
  actualVGNameOnTheNode: "vg-1" # имя LVM VG, которая будет создана на узле из указанных выше блочных устройств
EOF
```

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Operational`:

```shell
kubectl get lvg vg-1-on-worker-0 -w
```

- Если ресурс перешел в состояние `Operational`, то это значит, что на узле `worker-0` из блочных устройств `/dev/vdd` и `/dev/vdb` была создана LVM VG с именем `vg-1`.

- Далее создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-1`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-1"
spec:
  type: Local
  blockDeviceNames:
    - dev-49ff548dfacba65d951d2886c6ffc25d345bb548
  actualVGNameOnTheNode: "vg-1"
EOF
```

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Operational`:

```shell
kubectl get lvg vg-1-on-worker-1 -w
```

- Если ресурс перешел в состояние `Operational`, то это значит, что на узле `worker-1` из блочного устройства `/dev/vde` была создана LVM VG с именем `vg-1`.

- Далее создать ресурс [LVMVolumeGroup](../../sds-node-configurator/stable/cr.html#lvmvolumegroup) для узла `worker-2`:

```shell
kubectl apply -f - <<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: LvmVolumeGroup
metadata:
  name: "vg-1-on-worker-2"
spec:
  type: Local
  blockDeviceNames:
    - dev-75d455a9c59858cf2b571d196ffd9883f1349d2e
  actualVGNameOnTheNode: "vg-1"
EOF
```

- Дождаться, когда созданный ресурс `LVMVolumeGroup` перейдет в состояние `Operational`:

```shell
kubectl get lvg vg-1-on-worker-2 -w
```

- Если ресурс перешел в состояние `Operational`, то это значит, что на узле `worker-2` из блочного устройства `/dev/vdd` была создана LVM VG с именем `vg-1`.

- Теперь, когда у нас на узлах созданы нужные LVM VG, мы можем создать из них [ReplicatedStoragePool](./cr.html#replicatedstoragepool):

```yaml
kubectl apply -f -<<EOF
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStoragePool
metadata:
  name: data
spec:
  type: LVM
  lvmVolumeGroups: # Здесь указываем имена ресурсов LvmVolumeGroup, которые мы создавали ранее
    - name: vg-1-on-worker-0
    - name: vg-1-on-worker-1
    - name: vg-1-on-worker-2
EOF

```

- Дождаться, когда созданный ресурс `ReplicatedStoragePool` перейдет в состояние `Completed`:

```shell
kubectl get rsp data -w
```

- Проверить, что в LINSTOR создался Storage Pool `data` на узлах `worker-0`,  `worker-1` и `worker-2`:

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
- Использование стоковых ядер, поставляемых вместе с [поддерживаемыми дистрибутивами](https://deckhouse.ru/documentation/v1/supported_versions.html#linux);
- Использование сети 10Gbps.
- Не использовать другой SDS (Software defined storage) для предоставления дисков нашему SDS

### Рекомендации

- Не использовать RAID. Причины подробнее раскрыты в нашем [FAQ](./faq.html#почему-вы-не-рекомендуете-использовать-raid).

- Использовать локальные "железные" диски. Причины подробнее раскрыты в нашем [FAQ](./faq.html#почему-вы-рекомендуете-использовать-локальные-диски).
