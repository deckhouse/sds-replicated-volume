<!--
How to read this file.

The new control plane of sds-replicated-volume (its own controller, without LINSTOR) has not been
released yet, but its documentation was already written into the module docs. Until the release,
that documentation is kept here: docs/newcontrolplane/ is not rendered on the documentation site,
the same way crds/newcontrolplane/ keeps the new control plane CRDs out of it.

The file collects fragments cut out of every document in docs/, grouped by the source document
(one level-2 heading per document). Every fragment is preceded by a comment that names:
  - Where: the section of the source document the fragment belongs to;
  - Replaced by: what the published document has in its place now (if anything) -- the text
    describing the released (LINSTOR-based) control plane.

When the new control plane is released, put every fragment back where its comment says,
replacing the text named in "Replaced by", and delete this file together with its _RU pair.
New documentation of the new control plane written before the release goes here as well, in
the same format, and always into both files: SUMMARY.md and SUMMARY_RU.md mirror each other.
Fragments keep the heading levels they have in the source document.
-->

## USAGE_RU.md

<!--
Where: docs/USAGE_RU.md, «Создание ресурса ReplicatedStorageClass», примечание после абзаца о том, что контроллер создаёт StorageClass
Replaced by: примечание о том, что поля spec нельзя изменить после создания
-->

> Обратите внимание, что большинство полей `spec` ресурса ReplicatedStorageClass являются **неизменяемыми** после создания. Изменить у существующего ресурса можно только параметры репликации (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), `configurationRolloutStrategy`, `eligibleNodesConflictResolutionStrategy`, а также `reclaimPolicy` (StorageClass при этом пересоздаётся с новой политикой); изменение любого другого поля (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector` и т. д.) при обновлении будет отклонено.

<!--
Where: docs/USAGE_RU.md, текст раздела «Обновление ресурса ReplicatedStorageClass» вместе с подразделами
Replaced by: абзац о том, что параметры StorageClass поменять невозможно
-->

Большинство полей `spec` неизменяемы после создания, и попытка их изменить отклоняется с ошибкой, называющей поле; для изменения такого поля (например `storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector`) ресурс нужно пересоздать. Параметры репликации и `reclaimPolicy` изменяемы: правка `replication` позволяет выполнить миграцию r3→r2, описанную ниже, а правка `reclaimPolicy` заставляет модуль пересоздать StorageClass с новой политикой.

##### Миграция томов с трёх реплик (r3) на две реплики + tie-breaker (r2)

Изменение `spec.replication` у существующего ReplicatedStorageClass меняет целевой layout сразу **у всех** томов этого класса. Чтобы мигрировать с `ConsistencyAndAvailability` (три реплики данных, layout `3D`) на `Availability` (две реплики данных плюс diskless tie-breaker, layout `2D+1TB`):

1. Измените класс:

   ```shell
   kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"replication":"Availability"}}'
   ```

2. Контроллер мигрирует каждый том на месте: одна diskful-реплика ретайпится в tie-breaker (без полного ресинка и без переноса данных), её логический том освобождается. Прогресс по каждому тому виден в колонках `LAYOUT` и `CONVERGED` (`CONVERGED` — только в `-o wide`):

   ```shell
   kubectl get replicatedvolume -o wide
   kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}{end}{"\n"}'
   ```

   Том мигрирован, когда `MembershipLayoutConverged` = `True/Converged`, а `status.membershipLayout` = `2D+1TB`.

3. Прогресс по классу в целом виден через condition `ConfigurationRolledOut` и счётчики `status.volumes`:

   ```shell
   kubectl get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.volumes}{"\n"}'
   ```

   Раскатка завершена, когда `ConfigurationRolledOut` = `True`; это происходит ровно тогда, когда `status.volumes.pendingObservation` и `status.volumes.staleConfiguration` оба равны `0`.

   Равенства `aligned` и `total` ждать не нужно. В раскатке участвуют только тома, берущие конфигурацию у класса; том, переведённый в `spec.configurationMode: Manual`, несёт собственную конфигурацию, поэтому класс ничего ему не раскатывает и его не ждёт. При этом такие тома продолжают учитываться в `total`, и раскатка класса завершается при `aligned` меньше `total`.

**Требования.** Для layout `2D+1TB` кроме двух diskful-узлов нужен узел под tie-breaker: не менее 3 узлов для топологии `Ignored`, не менее 3 зон для `TransZonal` либо не менее 3 узлов в зоне тома для `Zonal`. Это те же требования, что и для `3D`, поэтому миграция r3→r2 их не повышает.

**Раскатка только на новые тома.** По умолчанию (`configurationRolloutStrategy.type: RollingUpdate`) правка конфигурации применяется ко всем томам класса. Чтобы она применялась только к вновь создаваемым томам, переключите стратегию на `NewVolumesOnly`:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"NewVolumesOnly","rollingUpdate":null}}}'
```

Тома, у которых конфигурация уже есть, сохраняют её. Такой том видит новую конфигурацию (класс не зависает в ожидании тома), но не применяет её, и репортит:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/NewerConfigurationHeld: ... has a newer configuration (generation N); the volume keeps its configuration (generation M) ...
```

Такие тома попадают в счётчик `status.volumes.staleConfiguration` класса, а `ConfigurationRolledOut` становится `False/ConfigurationRolloutDisabled`. Удержание намеренное и сохраняется, даже если удерживаемая конфигурация перестала соответствовать кластеру: чтобы выпустить том из этого состояния, переключите стратегию обратно на `RollingUpdate` (все удерживаемые тома раскатаются обычным путём) либо пересоздайте том. Переключение с `RollingUpdate` на `NewVolumesOnly` ничего не откатывает — уже применённая конфигурация остаётся применённой.

**Ограничение параллельности раскатки.** При `RollingUpdate` параметр `configurationRolloutStrategy.rollingUpdate.maxParallel` (по умолчанию `5`) задаёт, сколько томов класса мигрируют одновременно:

```shell
kubectl patch replicatedstorageclass <RSC_NAME> --type=merge -p '{"spec":{"configurationRolloutStrategy":{"type":"RollingUpdate","rollingUpdate":{"maxParallel":2}}}}'
```

Тома, которым ещё нужна новая конфигурация, упорядочены по имени, и свободные слоты занимают первые из них — сразу все, то есть при `maxParallel: 2` первые два тома мигрируют параллельно. Слот освобождается, когда его том репортит `MembershipLayoutConverged=True/Converged`, и переходит к следующему имени в этом порядке. Ожидающие тома сохраняют собственную конфигурацию и репортят:

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{range .status.conditions[?(@.type=="ConfigurationReady")]}{.status}/{.reason}: {.message}{end}{"\n"}'
# False/ConfigurationRolloutInProgress: ... rolls its configuration (generation N) out to at most 2 volume(s) at a time ...
```

Ожидающие тома попадают в счётчик `status.volumes.staleConfiguration` класса, поэтому `ConfigurationRolledOut` остаётся `False/ConfigurationRolloutInProgress`, пока не мигрирует весь класс. Уменьшение `maxParallel` не останавливает уже мигрирующие тома — оно лишь не пускает в раскатку новые. Том, который не может сойтись на новой конфигурации (см. ограничения ниже), удерживает свой слот бессрочно, и в этом и состоит смысл параметра: он ограничивает не только скорость раскатки удачной правки, но и число томов, до которых доберётся неудачная.

**Рост класса (r2→r3).** Изменение `replication` с `Availability` на `ConsistencyAndAvailability` тоже не требует ручных шагов. Каждый том класса сам проходит путь `2D+1TB → 3D+1TB → 3D`: control plane создаёт третью копию, дожидается её синхронизации и только потом удаляет tie-breaker, который в `r3` не нужен, — именно в таком порядке, чтобы том ни в один момент не остался с двумя копиями данных без свидетеля. `MembershipLayoutConverged` всё это время называет текущий шаг (`creating diskful replica`, `waiting for a free resync slot`, `waiting for the diskful copies to synchronize before removing a surplus tie-breaker`, `tie-breaker ... is terminating`), а алерт `D8ReplicatedVolumeLayoutDegraded` молчит: он срабатывает только на `TransitionUnsupported` и `CannotConverge`.

Слот раскатки том держит до самого конца — включая ресинк, — поэтому `maxParallel` ограничивает число томов класса, одновременно находящихся в середине роста. Если третью копию некуда разместить, том репортит `False/CannotConverge` со сводкой от scheduler'а и сохраняет tie-breaker: неудача размещения никогда не стоит тому отказоустойчивости.

**Ограничения.**

- Откат правки, пока том ещё мигрирует, не отменяет уже запущенный перевод реплики в tie-breaker. В зависимости от момента отката том останется либо в раскладке `2D+1TB` при желаемой `3D` — это ровно тот самый рост, описанный выше, и том сойдётся сам, — либо с репликой, у которой `spec.type` застрял в значении `TieBreaker`, тогда как раскладка по-прежнему `3D` (`MembershipLayoutConverged=False/Converging`, имя реплики указано в сообщении condition). Второй случай сам в `Converged` уже не вернётся; данные не теряются, а реплику нужно восстановить одним патчем: вернуть `spec.type` в `Diskful` и одновременно вернуть поля backing volume (`spec.lvmVolumeGroupName`, а для thin-пула ещё и `spec.lvmVolumeGroupThinPoolName`), взяв значения из `status.datamesh.members` тома.
- `eligibleNodesConflictResolutionStrategy.rollingRepair.maxParallel` принимается, но не реализован: перенос томов с узлов, переставших быть eligible, не ограничивается по параллельности. Это другой параметр, не тот `maxParallel` из конфигурационной раскатки выше — он-то как раз работает.

<!--
Where: docs/USAGE_RU.md, отдельный раздел между «Обновление ресурса ReplicatedStorageClass» и «Удаление ресурса ReplicatedStorageClass»
Replaced by: nothing
-->

#### Восстановление потерянной реплики

Том, потерявший diskful-реплику — узел удалили, реплику force-removed, член ушёл из datamesh по любой другой причине, — восстанавливается сам. Control plane создаёт замещающий `ReplicatedVolumeReplica`, scheduler его размещает, datamesh включает его в состав, данные ресинхронизируются. Ручных шагов нет, и порядок один и тот же независимо от того, что именно убрало члена.

Пока идёт восстановление, том показывает прогресс в `MembershipLayoutConverged`, например `False/Converging: creating diskful replica: have 2D, want 3D`. Алерт `D8ReplicatedVolumeLayoutDegraded` при этом молчит: он срабатывает только на `TransitionUnsupported` и `CannotConverge`.

```shell
kubectl get replicatedvolume <RV_NAME> -o jsonpath='{.status.membershipLayout} {range .status.conditions[?(@.type=="MembershipLayoutConverged")]}{.status}/{.reason}: {.message}{end}{"\n"}'
```

О чём стоит знать заранее:

- **По одной копии за раз.** Том, потерявший две реплики, восстанавливается в два круга: вторая замена создаётся только после того, как первая вошла в состав. Ёмкость заранее не резервируется.
- **Размещать некуда.** Если ни один подходящий узел не может принять реплику, том репортит `False/CannotConverge` со сводкой от scheduler'а, и алерт срабатывает — это как раз тот случай, когда нужен человек. Реплика уже создана и будет размещена, как только появится узел или ёмкость; создавать что-либо руками не нужно.

Что автоматически **не** восстанавливается, потому что control plane никогда не удаляет *копию* и её данные: том, у которого копий больше, чем требует конфигурация, и реплика, чей член всё ещё в datamesh, но диск потерян (это замена, а не добавление). Лишний *tie-breaker* — другое дело: он не несёт данных, и control plane снимает его сам, как только число копий совпало с конфигурацией и все они синхронны.

## FAQ_RU.md

<!--
Where: docs/FAQ_RU.md, последний подраздел «Troubleshooting»
Replaced by: nothing
-->

### Диагностика рассинхронизации данных DRBD или потери кворума (новый control plane)

Раздел относится к кластерам на новом control plane (плоскость управления) модуля `sds-replicated-volume` — с собственным контроллером, без LINSTOR. В этом режиме LINSTOR-клиента нет: DRBD-ресурсами управляют напрямую через `drbdsetup`, а поузловой агент работает в DaemonSet `agent` (метка `app=agent`). Контейнер `agent` — distroless (без оболочки shell), поэтому бинарники `drbd*` вызываются напрямую (`exec ... -- drbdsetup ...`, а не `-- bash`).

DRBD-алерты (`D8DrbdPeerDeviceIsOutOfSync`, `D8DrbdDeviceHasNoQuorum`, `D8DrbdDeviceIsNotConnected`) несут две полезные метки:

- `name` — имя DRBD-ресурса; используйте его как аргумент `<resource>` ниже;
- `conn_name` — затронутое соединение с пиром, в виде `peer-N`, где `N` — это peer node-id (идентификатор узла-пира), который использует `drbdsetup` (проверить можно в `drbdsetup status --verbose`, там выводится `peer-N node-id:X`).

Все команды ниже используют `d8 k` как точку входа в kubectl.

#### Просмотр состояния ресурса

Найдите под `agent` на узле и выведите статус ресурса (подставьте `<node>` и `<resource>`):

```shell
POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<node> -l app=agent -o name)
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --verbose --statistics
```

Обратите внимание: `drbdadm status <resource>` здесь не работает (новый control plane не пишет файлы `/etc/drbd.d/*.res`), всегда используйте `drbdsetup`.

#### Восстановление потерянного кворума (`D8DrbdDeviceHasNoQuorum`)

Diskless-Primary (бездисковый первичный) приостанавливает свой ввод-вывод при потере кворума. Частая причина — diskful-реплика (реплика с диском), застрявшая в пересинхронизации у самого конца (`replication:SyncTarget`, при этом `out-of-sync`/`received` не двигаются, иногда встречается повреждённое значение `done:` намного больше 100%), из-за чего слишком мало реплик в состоянии `UpToDate`.

Переподключите застрявшее соединение с пиром на застрявшем (`Inconsistent`) узле, чтобы перезапустить пересинхронизацию. `<peer-node-id>` — это node-id актуального источника данных (`UpToDate`):

```shell
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
```

Соединение восстановится автоматически. Как только вторая реплика достигнет `UpToDate`, кворум восстановится и Primary разморозит ввод-вывод (`drbdsetup status <resource>` покажет `quorum:yes`, `blocked:no`).

Это безопасно: актуальный источник данных не затрагивается, `Inconsistent`-реплика не участвует в кворуме, а ввод-вывод на Primary и так уже приостановлен — переподключение не создаёт новой деградации и не может привести к потере данных.

#### Очистка рассинхронизированных данных (`D8DrbdPeerDeviceIsOutOfSync`)

Когда оба конца соединения в `UpToDate`, но остаточные байты `out-of-sync` «заморожены», DRBD не пересинхронизирует их сам (совпадающие generation UUID означают, что пересинхронизация не запускается). Переподключение пира форсирует bitmap-based resync (пересинхронизацию по битовой карте) ровно этих экстентов:

```shell
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
# out-of-sync должен обнулиться:
d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --statistics
```

Если остаётся небольшой остаток:

1. Запустите онлайн-верификацию этого соединения (читает оба диска и сверяет совпадающие/различающиеся блоки):

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup verify <resource> <peer-node-id> 0
   ```

2. Если остаток сохраняется, а диск пира в `UpToDate`, выполните полное перезеркалирование копии пира. На узле-пире (том, что указан в `conn_name`) инвалидируйте его локальную копию — она станет `Inconsistent` и выполнит полную пересинхронизацию от актуальной реплики. Кворум на время resync держат остальные `UpToDate`-реплики:

   ```shell
   PEER_POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<peer-node> -l app=agent -o name)
   MIN=$(d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $PEER_POD -c agent -- drbdsetup status <peer-resource> --verbose | grep -oE 'minor:[0-9]+' | head -1 | cut -d: -f2)
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $PEER_POD -c agent -- drbdsetup invalidate $MIN
   ```

**Фантомный счётчик (phantom counter).** Если после полного перезеркалирования Primary всё ещё показывает крошечный `out-of-sync` к пиру, чьи данные только что были побитово перечитаны, — это остаточный артефакт учёта на стороне Primary, а не реальное расхождение: данные на всех репликах идентичны. Переподключение, верификация и перезеркалирование его не снимут — он сбрасывается только при деструктивном действии (failover — переключение роли — или перезагрузке узла-Primary). Для рабочего тома его безопасно оставить до ближайшего окна обслуживания.

<!--
Where: docs/FAQ_RU.md, ответ на «Я не могу обновить поля в spec у ресурса ReplicatedStorageClass. Это ожидаемое поведение?»
Replaced by: ответ о том, что изменение spec не поддерживается
-->

Частично. Большинство полей `spec` неизменяемы после создания, и их обновление отклоняется с ошибкой, называющей поле. Однако параметры репликации (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`), поля `configurationRolloutStrategy`/`eligibleNodesConflictResolutionStrategy`, а также `reclaimPolicy` (StorageClass при этом пересоздаётся с новой политикой) **можно** менять у существующего ресурса. Именно изменением `replication` выполняется миграция томов между layout'ами — например с `ConsistencyAndAvailability` (`3D`) на `Availability` (`2D+1TB`); см. раздел «Обновление ресурса ReplicatedStorageClass» в руководстве по использованию. Все остальные поля (`storage`, `topology`, `zones`, `volumeAccess`, `nodeLabelSelector` и т. д.) остаются неизменяемыми — чтобы их изменить, ресурс нужно пересоздать.

<!--
Where: docs/FAQ_RU.md, текст раздела «Сколько места тома резервируется под суперпользователя?»
Replaced by: тот же раздел, переписанный для выпущенного control plane (аннотация задаётся только при создании)
-->

По умолчанию — нисколько: том модуля форматируется с `-m 0`, весь его объём доступен нагрузке.

Если классический 5-процентный резерв ext4 всё же нужен — например, чтобы привилегированный процесс мог писать в файловую систему после того, как нагрузка заняла её целиком, — задайте процент аннотацией на ресурсе `ReplicatedStorageClass`:

```shell
d8 k annotate replicatedstorageclass <name> storage.deckhouse.io/ext4-reserved-percent=5
```

Значение — целое число процентов от 0 до 50; некорректное значение отклоняется валидирующим вебхуком.

Резерв применяется только к томам, созданным после установки аннотации: уже существующие файловые системы сохраняют тот резерв, с которым были созданы. Блоки для суперпользователя резервируют только файловые системы семейства ext, поэтому для томов XFS аннотация игнорируется.

Изменение аннотации приводит к пересозданию StorageClass контроллером, поскольку поле `parameters` у существующего StorageClass в Kubernetes иммутабельно. На уже созданные тома и PVC это не влияет.

На legacy-плоскости управления (LINSTOR) та же аннотация применяется через property `FileSystem/MkfsParameters` ресурс-дефиниции LINSTOR. Единственное исключение — аварийный путь, создающий файловую систему, когда LINSTOR её не создал: там всегда действует значение по умолчанию 0%, поскольку процент до этого кода не доходит.
