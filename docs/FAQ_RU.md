---
title: "Модуль SDS-DRBD: FAQ"
description: Диагностика проблем LINSTOR. Когда следует использовать LVM, а когда LVMThin? Производительность и надежность LINSTOR, сравнение с Ceph. Как добавить существующий LVM или LVMThin-пул в LINSTOR? Как настроить Prometheus на использование хранилища LINSTOR? Вопросы по работе контроллера.
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только в следующих случаях:
- при использовании стоковых ядер, поставляемых вместе с [поддерживаемыми дистрибутивами](https://deckhouse.ru/documentation/v1/supported_versions.html#linux);
- при использовании сети 10Gbps.

Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

## Когда следует использовать LVM, а когда LVMThin?

- LVM проще и обладает производительностью, сравнимой с производительностью накопителя;
- LVMThin позволяет использовать snapshot'ы и overprovisioning, но существенно медленнее.


## Как получить информацию об используемом пространстве?

Есть два варианта:

- Через дашборд Grafana: перейдите **Dashboards --> Storage --> LINSTOR/DRBD**  
  В правом верхнем углу вы найдете информацию об используемом пространстве в кластере.

  > **Внимание!** Эта информация отражает состояние всего *сырого* пространства в кластере.
  > То есть если вы создаете тома в двух репликах, то эти значения стоит поделить на два. Это нужно, чтобы получить примерное представление о том, сколько таких томов может быть размещено в вашем кластере.

- Через командный интерфейс LINSTOR:

  ```shell
  kubectl exec -n d8-sds-drbd deploy/linstor-controller -- linstor storage-pool list
  ```

  > **Внимание!** Эта информация отражает состояние *сырого* пространства для каждого узла в кластере.
  > То есть если вы создаете тома в двух репликах, то эти две реплики обязательно должны целиком поместиться на двух узлах вашего кластера.

## Как назначить StorageClass по умолчанию?
В соответствующем пользовательском ресурсе [DRBDStorageClass](./cr.html#drbdstorageclass) в поле `spec.isDefault` указать `true`.  

## Как добавить существующую LVM Volume Group или LVMThin-пул?

1. Вручную назначить на Volume Group тег `storage.deckhouse.io/enabled=true`:

```shell
vgchange myvg-0 --add-tag storage.deckhouse.io/enabled=true
```

2. Данный VG будет автоматически обнаружен и в кластере для него будет создан соответствующий ресурс `LVMVolumeGroup`.

3. Этот ресурс можно указать в параметрах [DRBDStoragePool](./cr.html#drbdstoragepool) в поле `spec.lvmVolumeGroups[].name` (для LVMThin-пула необходимо дополнительно указать имя в `spec.lvmVolumeGroups[].thinPoolName`).

## Как выгнать ресурсы с узла?

* Загрузите скрипт `evict.sh` на хост, имеющий доступ к API Kubernetes с правами администратора (для работы скрипта потребуются установленные `kubectl` и `jq`):

  * Последнюю версию скрипта можно скачать с GitHub:

    ```shell
    curl -fsSL -o evict.sh https://raw.githubusercontent.com/deckhouse/deckhouse/main/modules/041-linstor/tools/evict.sh
    chmod 700 evict.sh
    ```

  * Также скрипт можно скачать из пода `deckhouse`:

    ```shell
    kubectl -n d8-system cp -c deckhouse $(kubectl -n d8-system get po -l app=deckhouse -o jsonpath='{.items[0].metadata.name}'):/deckhouse/modules/041-linstor/tools/evict.sh ./evict.sh
    chmod 700 evict.sh
    ```

* Исправьте все ошибочные ресурсы LINSTOR в кластере. Чтобы найти их, выполните следующую команду:

  ```shell
  kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor resource list --faulty
  ```

* Убедитесь, что все поды в пространстве имен `d8-sds-drbd` находятся в состоянии *Running*:

  ```shell
  kubectl -n d8-sds-drbd get pods | grep -v Running
  ```

### Выгнать ресурсы с узла без удаления его из LINSTOR и Kubernetes

Запустите скрипт `evict.sh` в интерактивном режиме, указав режим удаления `--delete-resources-only`:

```shell
./evict.sh --delete-resources-only
```

Для запуска скрипта `evict.sh` в неинтерактивном режиме необходимо добавить флаг `--non-interactive` при его вызове, а также имя узла, с которого необходимо выгнать ресурсы. В этом режиме скрипт выполнит все действия без запроса подтверждения от пользователя. Пример вызова:

```shell
./evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Важно!** После завершении работы скрипта узел в Kubernetes останется в статусе *SchedulingDisabled*, а в LINSTOR у данного узла будет выставлен параметр *AutoplaceTarget=false*, что запретит планировщику LINSTOR создавать на этом узле ресурсы.

Если необходимо снова разрешить размещать ресурсы и поды на узле, нужно выполнить команды:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor node set-property "worker-1" AutoplaceTarget
kubectl uncordon "worker-1"
```

Проверить параметр *AutoplaceTarget* у всех узлов можно так (поле AutoplaceTarget будет пустым у тех узлов, на которых разрешено размещать ресурсы LINSTOR):

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor node list -s AutoplaceTarget
```

## Диагностика проблем

Проблемы могут возникнуть на разных уровнях работы компонентов.
Эта простая шпаргалка поможет вам быстро сориентироваться при диагностике различных проблем с томами, созданными в LINSTOR:

![LINSTOR шпаргалка](./images/linstor-debug-cheatsheet.ru.svg)
<!--- Исходник: https://docs.google.com/drawings/d/19hn3nRj6jx4N_haJE0OydbGKgd-m8AUSr0IqfHfT6YA/edit --->

Некоторые типичные проблемы описаны ниже.

### linstor-node не может запуститься из-за невозможности загрузки drbd-модуля

Проверьте состояние подов `linstor-node`:

```shell
kubectl get pod -n d8-sds-drbd -l app=linstor-node
```

Если вы видите, что некоторые из них находятся в состоянии `Init`, проверьте версию drbd и логи bashible на узле:

```shell
cat /proc/drbd
journalctl -fu bashible
```

Наиболее вероятные причины, почему он не может загрузить модуль ядра:

- Возможно, у вас уже загружена in-tree-версия модуля DRBDv8, тогда как модуль требует DRBDv9.
  Проверить версию загруженного модуля: `cat /proc/drbd`. Если файл отсутствует, значит, модуль не загружен и проблема не в этом.

- Возможно, у вас включен Secure Boot.
  Так как модуль DRBD, который мы поставляем, компилируется динамически для вашего ядра (аналог dkms), он не имеет цифровой подписи.
  На данный момент мы не поддерживаем работу модуля DRBD в конфигурации с Secure Boot.

### Под не может запуститься из-за ошибки `FailedMount`

#### **Под завис на стадии `ContainerCreating`**

Если под завис на стадии `ContainerCreating`, а в выводе `kubectl describe pod` есть ошибки вида:

```text
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

значит, устройство все еще смонтировано на одном из других узлов.

Проверить это можно с помощью следующей команды:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor resource list -r pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d
```

Флаг `InUse` укажет, на каком узле используется устройство, на этом узле потребуется вручную отмонтировать диск.

#### **Ошибки вида `Input/output error`**

Такие ошибки обычно возникают на стадии создания файловой системы (mkfs).

Проверьте `dmesg` на узле, где запускается под:

```shell
dmesg | grep 'Remote failed to finish a request within'
```

Если вывод команды не пустой (в выводе `dmesg` есть строки вида *"Remote failed to finish a request within ..."*), скорее всего, ваша дисковая подсистема слишком медленная для нормального функционирования DRBD.

## Я удалил ресурс DRBDStoragePool, но соответствующий Storage Pool в бэкенде LINSTOR остался. Так и должно быть?
Да, в настоящий момент модуль `SDS-DRBD` не обрабатывает операции при удалении ресурса `DRBDStoragePool`.

## Я не могу обновить поля в spec у ресурса DRBDStorageClass. Это ожидаемое поведение? 
Да, поведение ожидаемое. В `spec` можно изменять только поле `isDefault`. Остальные поля в `spec` ресурса сделаны неизменяемыми.

## При удалении ресурса DRBDStorageClass не удаляется его дочерний StorageClass в Kubernetes. Что делать?
Удаление дочернего StorageClass происходит только в случае, если статус ресурса DRBDStorageClass `Created`. В ином случае потребуется либо восстановить рабочее состояние ресурса DRBDStorageClass, либо удалить StorageClass самостоятельно.

## Я обратил внимание, что при создании Storage Pool / StorageClass в соответствующем ресурсе была отображена ошибка, а после все исправилось и нужная мне сущность создалась. Это ожидаемое поведение?
Да, это поведение ожидаемо. Модуль автоматически повторит выполнение неудачной операции, если причиной ошибки послужили независящие от модуля обстоятельства (например, «моргнул» kube-apiserver).

## Я не нашел ответа на свой вопрос и испытываю проблемы с работой модуля. Что делать? 
Информация о причинах неудавшейся операции должна быть отображена в поле `status.reason` ресурсов `DRBDStoragePool` и `DRBDStorageClass`. 
Если предоставленной информации не хватает для идентификации проблемы, вы можете обратиться к логам sds-drbd-controller.

## Миграция на DRBDStorageClass

StorageClass'ы в данном модуле управляются через ресурс DRBDStorageClass. Вручную StorageClass'ы создаваться не должны.

При миграции с модуля Linstor необходимо удалить старые StorageClass'ы и создать новые через ресурс DRBDStorageClass в соответствии с таблицей.

Обратите внимание, что в старых StorageClass нужно смотреть опцию из секции parameter самого StorageClass, а указывать соответствующую опцию при создании нового необходимо в DRBDStorageClass.

| параметр StorageClass                     | DRBDStorageClass      | Параметр по умолчанию | Примечания                                                     |
|-------------------------------------------|-----------------------|-|----------------------------------------------------------------|
| linstor.csi.linbit.com/placementCount: "1" | replication: "None"   | | Будет создаваться одна реплика тома с данными                  |
| linstor.csi.linbit.com/placementCount: "2" | replication: "Availability" | | Будет создаваться две реплики тома с данными.                  |
| linstor.csi.linbit.com/placementCount: "3" | replication: "ConsistencyAndAvailability" | да | Будет создаваться три реплики тома с данными                   |
| linstor.csi.linbit.com/storagePool: "name" | storagePool: "name"   | | Название используемого storage pool для хранения               |
| linstor.csi.linbit.com/allowRemoteVolumeAccess: "false" | volumeAccess: "Local" | | Запрещен удаленный доступ Pod к томам с данными (только локальный доступ к диску в пределах Node) |

Кроме них, можно использовать параметры:

- reclaimPolicy (Delete, Retain) - соответствует параметру reclaimPolicy у старого StorageClass
- zones - перечисление зон, которые нужно использовать для размещения ресурсов (прямое указание названия зон в облаке). Обратите внимание, что удаленный доступ пода к тому с данными возможен только в пределах одной зоны!
- volumeAccess может принимать значения "Local" (доступ строго в пределах узла), "EventuallyLocal" (реплика данных будет синхронизироваться на узле с запущенным подом спустя некоторое время после запуска), "PreferablyLocal" (удаленный доступ пода к тому с данными разрешен, volumeBindingMode: WaitForFirstConsumer), "Any" (удаленный доступ пода к тому с данными разрешен, volumeBindingMode: Immediate)
- При необходимости использовать volumeBindingMode: Immediate нужно выставлять параметр DRBDStorageClass volumeAccess равным Any
