---
title: "Модуль sds-replicated-volume: FAQ"
description: Диагностика проблем LINSTOR. Когда следует использовать LVM, а когда LVMThin? Производительность и надежность LINSTOR, сравнение с Ceph. Как добавить существующий LVM или LVMThin-пул в LINSTOR? Как настроить Prometheus на использование хранилища LINSTOR? Вопросы по работе контроллера.
---

{{< alert level="warning" >}}
Работоспособность модуля гарантируется только при соблюдении [требований](./readme.html#системные-требования-и-рекомендации).
Работоспособность модуля в других условиях возможна, но не гарантируется.
{{< /alert >}}

## Когда следует использовать LVM, а когда LVMThin?

- LVM проще и обладает высокой производительностью, сравнимой с производительностью накопителя, но не позволяет использовать snapshot'ы;
- LVMThin позволяет использовать snapshot'ы и overprovisioning, но производительность ниже, чем у LVM.

## Как получить информацию об используемом пространстве?

Существует два варианта:

1. Через дашборд Grafana:

* Перейдите **Dashboards --> Storage --> LINSTOR/DRBD**  
  В правом верхнем углу находится информация об используемом пространстве в кластере.

  > **Внимание!** Эта информация отражает состояние всего *сырого* пространства в кластере. Если необходимо создать тома в двух репликах, то эти значения стоит делить на два, чтобы получить представление - сколько таких томов разместить в кластере.

2. Через командный интерфейс LINSTOR с помощью команды:

  ```shell
  kubectl exec -n d8-sds-replicated-volume deploy/linstor-controller -- linstor storage-pool list
  ```

  > **Внимание!** Эта информация отражает состояние *сырого* пространства для каждого узла в кластере. Если создаются тома в двух репликах, то эти две реплики обязательно должны целиком поместиться на двух узлах вашего кластера.

## Как назначить StorageClass по умолчанию?

В соответствующем пользовательском ресурсе [ReplicatedStorageClass](./cr.html#replicatedstorageclass) в поле `spec.isDefault` указать `true`.  

## Как добавить существующую LVM Volume Group или LVMThin-пул?

1. Вручную назначьте на Volume Group LVM-тег `storage.deckhouse.io/enabled=true`:

   ```shell
   vgchange myvg-0 --add-tag storage.deckhouse.io/enabled=true
   ```

   Данный VG будет автоматически обнаружен и в кластере для него будет создан соответствующий ресурс `LVMVolumeGroup`.

2. Полученный ресурс укажите в параметрах [ReplicatedStoragePool](./cr.html#replicatedstoragepool) в поле `spec.lvmVolumeGroups[].name` (для LVMThin-пула необходимо дополнительно указать имя в `spec.lvmVolumeGroups[].thinPoolName`).

## Как выгнать DRBD-ресурсы с узла?

1. Загрузите скрипт `evict.sh` на хост, имеющий доступ к API Kubernetes с правами администратора (для работы скрипта потребуются установленные `kubectl` и `jq`):

  ```shell
   kubectl -n d8-sds-replicated-volume cp -c sds-replicated-volume-controller $(kubectl -n d8-sds-replicated-volume get po -l app=sds-replicated-volume-controller -o jsonpath='{.items[0].metadata.name}'):/tools/evict.sh ./evict.sh
   chmod 700 evict.sh
   ```

2. Исправьте все ошибочные ресурсы LINSTOR в кластере. Чтобы найти их, выполните следующую команду:

  ```shell
  kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor resource list --faulty
  ```

3. Убедитесь, что все поды в пространстве имен `d8-sds-replicated-volume` находятся в состоянии *Running*:

  ```shell
  kubectl -n d8-sds-replicated-volume get pods | grep -v Running
  ```

### Как удалить DRBD-ресурсы с узла, без удаления самого узла из LINSTOR и Kubernetes?

1. Запустите скрипт `evict.sh` в интерактивном режиме, указав режим удаления `--delete-resources-only`:

```shell
./evict.sh --delete-resources-only
```

Для запуска скрипта `evict.sh` в неинтерактивном режиме необходимо добавить флаг `--non-interactive` при его вызове, а также имя узла, с которого необходимо выгнать ресурсы. В этом режиме скрипт выполнит все действия без запроса подтверждения от пользователя. Пример вызова:

```shell
./evict.sh --non-interactive --delete-resources-only --node-name "worker-1"
```

> **Важно!** После завершении работы скрипта узел в Kubernetes останется в статусе *SchedulingDisabled*, а в LINSTOR у данного узла будет выставлен параметр *AutoplaceTarget=false*, что запретит планировщику LINSTOR создавать на этом узле ресурсы.

2. Если необходимо снова разрешить размещать DRBD-ресурсы и поды на узле, выполните команды:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor node set-property "worker-1" AutoplaceTarget
kubectl uncordon "worker-1"
```

3. Проверьте параметр *AutoplaceTarget* у всех узлов (поле AutoplaceTarget будет пустым у тех узлов, на которых разрешено размещать ресурсы LINSTOR):

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor node list -s AutoplaceTarget
```

## Диагностика проблем

Проблемы могут возникнуть на разных уровнях работы компонентов.
Эта шпаргалка поможет вам быстро сориентироваться при диагностике различных проблем с томами, созданными в LINSTOR:

![LINSTOR шпаргалка](./images/linstor-debug-cheatsheet.ru.svg)
<!--- Исходник: https://docs.google.com/drawings/d/19hn3nRj6jx4N_haJE0OydbGKgd-m8AUSr0IqfHfT6YA/edit --->

Некоторые типичные проблемы описаны ниже.

### linstor-node не может запуститься из-за невозможности загрузки drbd-модуля

1. Проверьте состояние подов `linstor-node`:

```shell
kubectl get pod -n d8-sds-replicated-volume -l app=linstor-node
```

2. Если некоторые поды находятся в состоянии `Init`, проверьте версию drbd и логи bashible на узле:

```shell
cat /proc/drbd
journalctl -fu bashible
```

Наиболее вероятные причины, почему bashible не может загрузить модуль ядра:

- Возможно, загружена in-tree-версия модуля DRBDv8, тогда как модуль требует DRBDv9.
  Проверить версию загруженного модуля можно командой: `cat /proc/drbd`. Если файл отсутствует, значит, модуль не загружен и проблема не в этом.

- Возможно, включен Secure Boot.
  Так как модуль DRBD компилируется динамически для вашего ядра (аналог dkms), он не имеет цифровой подписи.
  На данный момент мы не поддерживаем работу модуля DRBD в конфигурации с Secure Boot.

### Под не может запуститься из-за ошибки `FailedMount`

#### **Под завис на стадии `ContainerCreating`**

Если под завис на стадии `ContainerCreating`, а в выводе `kubectl describe pod` присутствуют ошибки вроде той, что представлена ниже, значит устройство смонтировано на одном из других узлов:

```text
rpc error: code = Internal desc = NodePublishVolume failed for pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d: checking
for exclusive open failed: wrong medium type, check device health
```

Проверьте, где используется устройство, с помощью следующей команды:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list -r pvc-b3e51b8a-9733-4d9a-bf34-84e0fee3168d
```

Флаг `InUse` укажет, на каком узле используется устройство, на этом узле потребуется вручную отмонтировать диск.

#### **Ошибки вида `Input/output error`**

Такие ошибки обычно возникают на стадии создания файловой системы (mkfs).

Проверьте `dmesg` на узле, где запускается под:

```shell
dmesg | grep 'Remote failed to finish a request within'
```

Если вывод команды не пустой (в выводе `dmesg` есть строки вида *"Remote failed to finish a request within ..."*), скорее всего, дисковая подсистема слишком медленная для нормального функционирования DRBD.

## Я удалил ресурс ReplicatedStoragePool, но соответствующий Storage Pool в бэкенде LINSTOR остался. Так и должно быть?

Да, в настоящий момент модуль `sds-replicated-volume` не обрабатывает операции при удалении ресурса `ReplicatedStoragePool`.

## Я не могу обновить поля в spec у ресурса ReplicatedStorageClass. Это ожидаемое поведение?

Да, поведение ожидаемое. В `spec` можно изменять только поле `isDefault`. Остальные поля в `spec` ресурса сделаны неизменяемыми.

## При удалении ресурса ReplicatedStorageClass не удаляется его дочерний StorageClass в Kubernetes. Что делать?

Если `StorageClass` находится в статусе `Created`, то его можно удалить. Если статус другой, нужно восстановить ресурс или удалить `StorageClass` вручную.

## При попытке создать Storage Pool / StorageClass возникла ошибка, но в итоге необходимая сущность успешно создалась. Такое допустимо?

Это поведение ожидаемо. Модуль автоматически повторит выполнение неудачной операции, если причиной ошибки послужили независящие от модуля обстоятельства (например, kube-apiserver временно стал недоступен).

## При выполнении команд в CLI, LINSTOR мне выдает ошибку "You're not allowed to change state of linstor cluster manually. Please contact tech support". Что делать?

Операции, которые требуют ручного вмешательства в LINSTOR, в модуле `sds-replicated-volume` частично или полностью автоматизированы. Поэтому модуль `sds-replicated-volume` ограничивает список разрешенных команд в LINSTOR. Например, автоматизировано создание Tie-Breaker, — LINSTOR иногда их не создает для ресурсов с двумя репликами. Список разрешенных команд можно посмотреть, выполнив следующую команду:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor --help
```

## Служебные поды компонентов sds-replicated-volume не создаются на нужной мне ноде.

С высокой вероятностью проблемы связаны с метками на нодах.

- Проверьте [dataNodes.nodeSelector](./configuration.html#parameters-datanodes-nodeselector) в настройках модуля:

```shell
kubectl get mc sds-replicated-volume -o=jsonpath={.spec.settings.dataNodes.nodeSelector}
```

- Проверьте селекторы, которые использует `sds-replicated-volume-controller`:

```shell
kubectl -n d8-sds-replicated-volume get secret d8-sds-replicated-volume-controller-config  -o jsonpath='{.data.config}' | base64 --decode

```

- В секрете `d8-sds-replicated-volume-controller-config` должны быть селекторы, которые указаны в настройках модуля, а так же дополнительно селектор `kubernetes.io/os: linux`.

- Необходимо проверить, что на нужной ноде есть все указанные в секрете `d8-sds-replicated-volume-controller-config` метки:

```shell
kubectl get node worker-0 --show-labels
```

- Если меток нет, то необходимо добавить метки через шаблоны в `NodeGroup` или на ноду.

- Если метки есть, то необходимо проверить, есть ли на нужной ноде метка `storage.deckhouse.io/sds-replicated-volume-node=`. Если метки нет, то необходимо проверить, запущен ли sds-replicated-volume-controller и если запущен, то проверить его логи:

```shell
kubectl -n d8-sds-replicated-volume get po -l app=sds-replicated-volume-controller
kubectl -n d8-sds-replicated-volume logs -l app=sds-replicated-volume-controller
```


## Я не нашел ответа на свой вопрос и испытываю проблемы с работой модуля. Что делать?

Информация о причинах неудавшейся операции должна отображаться в поле `status.reason` ресурсов `ReplicatedStoragePool` и `ReplicatedStorageClass`.
Если предоставленной информации не хватает для идентификации проблемы, вы можете обратиться к логам sds-replicated-volume-controller.

## Как выполнить миграцию со встроенного модуля [linstor](https://deckhouse.ru/documentation/v1.57/modules/041-linstor/) Deckhouse Kubernetes Platform на модуль sds-replicated-volume?

В процессе миграции будет недоступен control plane `LINSTOR` и его CSI. Это приведет к невозможности создания/расширения/удаления PV и созданию/удалению подов, использующих PV `LINSTOR`, на время проведения миграции.

> **Важно!** Миграция не затронет пользовательские данные, так как произойдет переезд в новый namespace и будут добавлены новые компоненты, которые в будущем исполнят функциональность LINSTOR по управлению томами.

### Порядок действий для миграции

1. Удостоверьтесь, что в кластере нет "плохих ресурсов" `LINSTOR`. Эта команда должна выводить пустой список:

```shell
alias linstor='kubectl -n d8-linstor exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

> **Внимание!** Важно починить все ресурсы `LINSTOR` перед миграцией.

2. Выключите модуль `linstor`:

```shell
kubectl patch moduleconfig linstor --type=merge -p '{"spec": {"enabled": false}}'
```

3. Дождитесь, когда namespace `d8-linstor` будет удален.

```shell
kubectl get namespace d8-linstor
```

4. Создайте ресурс `ModuleConfig` для `sds-node-configurator`.

```shell
kubectl apply -f -<<EOF
apiVersion: deckhouse.io/v1alpha1
kind: ModuleConfig
metadata:
  name: sds-node-configurator
spec:
  enabled: true
  version: 1
EOF
```

5. Дождитесь, когда модуль `sds-node-configurator` перейдет в состояние `Ready`.

```shell
kubectl get moduleconfig sds-node-configurator
```

6. Создайте ресурс `ModuleConfig` для `sds-replicated-volume`.

> **Внимание!** Если в настройках модуля `sds-replicated-volume` не будет указан параметр `settings.dataNodes.nodeSelector`, то значение для этого параметра при установке модуля `sds-replicated-volume` будет взято из модуля `linstor`. Если этот параметр не указан и там, то только в этом случае он останется пустым и все узлы кластера будут считаться узлами для хранения данных.

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

7. Дождитесь, когда модуль `sds-replicated-volume` перейдет в состояние `Ready`.

```shell
kubectl get moduleconfig sds-replicated-volume
```

8. Проверьте настройки модуля `sds-replicated-volume`:

```shell
kubectl get moduleconfig sds-replicated-volume -oyaml
```

9. Дождитесь, пока все поды в namespace `d8-sds-replicated-volume` и `d8-sds-node-configurator` перейдут в состояние `Ready` или `Completed`.

```shell
kubectl get po -n d8-sds-node-configurator
kubectl get po -n d8-sds-replicated-volume
```

10. Измените алиас к команде `linstor` и проверьте ресурсы `LINSTOR`:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

Если “плохие” ресурсы не обнаружены, значит миграция была успешной.

### Миграция на ReplicatedStorageClass.

StorageClass'ы в данном модуле управляются через ресурс `ReplicatedStorageClass`. Вручную StorageClass'ы создаваться не должны.

При миграции с модуля Linstor удалите старые StorageClass'ы и создайте новые через ресурс `ReplicatedStorageClass` в соответствии с таблицей, представленной ниже.

Обратите внимание, что в старых StorageClass нужно смотреть опцию из секции parameter самого StorageClass, а указывать соответствующую опцию при создании нового необходимо в `ReplicatedStorageClass`.

| параметр StorageClass                     | ReplicatedStorageClass      | Параметр по умолчанию | Примечания                                                     |
|-------------------------------------------|-----------------------|-|----------------------------------------------------------------|
| linstor.csi.linbit.com/placementCount: "1" | replication: "None"   | | Будет создаваться одна реплика тома с данными                  |
| linstor.csi.linbit.com/placementCount: "2" | replication: "Availability" | | Будет создаваться две реплики тома с данными.                  |
| linstor.csi.linbit.com/placementCount: "3" | replication: "ConsistencyAndAvailability" | да | Будет создаваться три реплики тома с данными                   |
| linstor.csi.linbit.com/storagePool: "name" | storagePool: "name"   | | Название используемого storage pool для хранения               |
| linstor.csi.linbit.com/allowRemoteVolumeAccess: "false" | volumeAccess: "Local" | | Запрещен удаленный доступ пода к томам с данными (только локальный доступ к диску в пределах узла) |

Кроме них, можно использовать параметры:

- reclaimPolicy (Delete, Retain) - соответствует параметру reclaimPolicy у старого StorageClass
- zones - перечисление зон, которые нужно использовать для размещения ресурсов (прямое указание названия зон в облаке). Обратите внимание, что удаленный доступ пода к тому с данными возможен только в пределах одной зоны!
- volumeAccess может принимать значения "Local" (доступ строго в пределах узла), "EventuallyLocal" (реплика данных будет синхронизироваться на узле с запущенным подом спустя некоторое время после запуска), "PreferablyLocal" (удаленный доступ пода к тому с данными разрешен, volumeBindingMode: WaitForFirstConsumer), "Any" (удаленный доступ пода к тому с данными разрешен, volumeBindingMode: Immediate)
- При необходимости использовать `volumeBindingMode: Immediate`, нужно выставлять параметр `ReplicatedStorageClass` volumeAccess равным Any.

Подробнее про работу с ресурсами `ReplicatedStorageClass` можно прочитать [здесь](./usage.html).

### Миграция на ReplicatedStoragePool

Ресурс `ReplicatedStoragePool` позволяет создавать `Storage Pool` в `LINSTOR`. Рекомендуется создать этот ресурс даже для уже существующих в LINSTOR `Storage Pool` и указать в этом ресурсе существующие `LVMVolumeGroup`. В этом случае контроллер увидит, что соответствующие `Storage Pool` созданы, и оставит их без изменений, а в поле `status.phase` созданного ресурса будет отображено значение `Created`. Подробнее про работу с ресурсами `LVMVolumeGroup` можно прочитать в документации модуля [sds-node-configurator](../../sds-node-configurator/stable/usage.html), а с ресурсами `ReplicatedStoragePool` [здесь](./usage.html).

## Как выполнить миграцию с модуля sds-drbd на модуль sds-replicated-volume?

В процессе миграции будет недоступен control plane модуля и его CSI. Это приведет к невозможности создания/расширения/удаления PV и созданию/удалению подов, использующих PV `DRBD`, на время проведения миграции.

> **Важно!** Миграция не затронет пользовательские данные, так как произойдет переезд в новый namespace и будут добавлены новые компоненты, которые в будущем исполнят функциональность модуля по управлению томами.

### Порядок действий для миграции

1. Удостоверьтесь, что в кластере нет "плохих ресурсов" `DRBD`. Эта команда должна выводить пустой список:

```shell
alias linstor='kubectl -n d8-sds-drbd exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

> **Внимание!** Важно починить все ресурсы `DRBD` перед миграцией.

2. Выключите модуль `sds-drbd`:

```shell
kubectl patch moduleconfig sds-drbd --type=merge -p '{"spec": {"enabled": false}}'
```

3. Дождитесь, когда namespace `d8-sds-drbd` будет удален.

```shell
kubectl get namespace d8-sds-drbd
```

4. Создайте ресурс `ModuleConfig` для `sds-replicated-volume`.

> **Внимание!** Если в настройках модуля `sds-replicated-volume` не будет указан параметр `settings.dataNodes.nodeSelector`, то значение для этого параметра при установке модуля `sds-replicated-volume` будет взято из модуля `sds-drbd`. Если этот параметр не указан и там, то только в этом случае он останется пустым и все узлы кластера будут считаться узлами для хранения данных.

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

5. Дождитесь, когда модуль `sds-replicated-volume` перейдет в состояние `Ready`.

```shell
kubectl get moduleconfig sds-replicated-volume
```

6. Проверьте настройки модуля `sds-replicated-volume`:

```shell
kubectl get moduleconfig sds-replicated-volume -oyaml
```

7. Дождитесь, пока все поды в namespace `d8-sds-replicated-volume` перейдут в состояние `Ready` или `Completed`.

```shell
kubectl get po -n d8-sds-replicated-volume
```

8. Измените алиас к команде `linstor` и проверьте ресурсы `DRBD`:

```shell
alias linstor='kubectl -n d8-sds-replicated-volume exec -ti deploy/linstor-controller -- linstor'
linstor resource list --faulty
```

Если “плохие” ресурсы не обнаружены, значит миграция была успешной.

> **Внимание!** Ресурсы DRBDStoragePool и DRBDStorageClass в процессе будут автоматически мигрированы на ReplicatedStoragePool и ReplicatedStorageClass, вмешательства пользователя для этого не требуется. Логика работы этих ресурсов не изменится. Однако, стоит проверить, не осталось ли в кластере ресурсов DRBDStoragePool или DRBDStorageClass, если после миграции они существуют - сообщите, пожалуйста, в нашу поддержку.

## Почему не рекомендуется использовать RAID для дисков, которые используются модулем `sds-replicated-volume`?

DRBD с количеством реплик больше 1 предоставляет по факту сетевой RAID. Использование RAID локально может быть неэффективным, так как:

- В несколько раз увеличивает оверхед по используемому пространству в случае использования RAID с избыточностью. Пример: используется `DBRDStorageClass` с `replication`, выставленном в `ConsistencyAndAvailability`. При таких настройках DRBD будет сохранять данные в трех репликах (по одной реплике на три разных хоста). Если на этих хостах будет использоваться RAID1, то для хранения 1 Гб данных потребуется суммарно 6 Гб места на дисках. RAID с избыточностью есть смысл использовать для упрощения обслуживания серверов в том случае, когда цена хранения не имеет значения. RAID1 в таком случае позволит менять диски на серверах без необходимости перемещения реплик данных с "проблемного" диска.

- В случае RAID0 прирост производительности будет незаметен, т. к. репликация данных будет осуществляться по сети и узким местом с высокой вероятностью будет именно сеть. Кроме того, уменьшение надежности хранилища на хосте потенциально будет приводить к недоступности данных, тк в DRBD переключение со сломавшейся реплики на здоровую происходит не мгновенно.

## Почему вы рекомендуете использовать локальные диски (не NAS)?

Мотивация: DRBD реплицирует данные по сети. Если будет использоваться NAS, то нагрузка на сеть увеличится и вырастет задержка на чтение/запись.

