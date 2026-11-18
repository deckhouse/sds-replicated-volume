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
Where: docs/USAGE_RU.md, a new section immediately before the application features section
Replaced by: nothing; this section is withheld until the new control plane is released
-->

### Выбор сети репликации (новый control plane)

При `settings.newControlPlane: true` сеть репликации выбирается при создании
ReplicatedStorageClass в поле `spec.systemNetworkNames`. Список принимает ровно
одну запись:

- `Internal` использует первый `InternalIP` из `status.addresses` каждой Node.
  Модуль `sdn` для этого не нужен. Если поле RSC не задано, контроллер заполняет
  его значением `["Internal"]`.
- Имя SystemNetwork использует соответствующий `SystemNetworkIP`, опубликованный
  модулем `sdn` в `NodeNetworkStatus/<node>.status.addresses`.

Следующий пример создаёт класс с сетью Internal и тремя заранее настроенными
LVMVolumeGroup на разных узлах:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: replicated-internal
spec:
  storage:
    type: LVM
    lvmVolumeGroups:
      - name: lvg-1
      - name: lvg-2
      - name: lvg-3
  failuresToTolerate: 1
  guaranteedMinimumDataRedundancy: 1
  topology: Ignored
  volumeAccess: Local
  reclaimPolicy: Delete
  systemNetworkNames: [Internal]
```

Для выделенной сети репликации сначала включите модуль `sdn` и создайте
SystemNetwork с настроенным `spec.ipam`. Её underlay должен соединять узлы,
которые будут участвовать в репликации. Дождитесь назначения SDN-адресов этим
узлам. Затем создайте **отдельный** класс по примеру выше, изменив имя и сеть:

```yaml
metadata:
  name: replicated-storage-net
spec:
  systemNetworkNames: [storage-net]
```

Этот фрагмент заменяет соответствующие поля полного примера; он не является
самостоятельным манифестом или обновлением `replicated-internal`.
RSC-контроллер автоматически создаст RSP с выбранной сетью.
При ручном создании RSP задайте такое же поле `spec.systemNetworkNames`
при создании.

#### Доступность и диагностика

Admission отклоняет RSC, ссылающийся на несуществующую SystemNetwork, сеть
без `spec.ipam` или SystemNetwork при недоступной поддержке SDN.
После включения SDN дождитесь обновления подов контроллера, агента и webhook
с поддержкой SDN, прежде чем создавать класс.

Узел-кандидат подходит для размещения, только если выбранная SystemNetwork
существует, не удаляется и в NodeNetworkStatus есть пригодный соответствующий
`SystemNetworkIP`. Готовность самой SystemNetwork не доказывает наличие адреса
на каждом узле. Проверяйте `status.eligibleNodes`, `status.message` и условие
Ready у RSP; сообщение отражает и частичное исключение узлов. Если требуемой сети
нет ни у одного кандидата, Ready имеет статус False с причиной
`NoNodesWithRequiredSystemNetworks`; RSC также сообщает об ошибке пула.
Если агент не может получить требуемый адрес при reconcile DRBDResource,
условие Configured имеет статус False с причиной `SystemNetworkAddressUnavailable`.

#### Текущие ограничения

- После установки `spec.systemNetworkNames` неизменяемо как в RSC, так и в RSP.
  Удаление установленного поля RSC также отклоняется. Для новых томов с другой
  сетью создайте отдельный класс, не изменяя существующий.
- Автоматическая миграция существующих томов между сетями и автоматический
  сетевой failover не поддерживаются. Наличие опубликованного адреса не является
  проверкой исправности линка.
- Не удаляйте SystemNetwork и не выключайте SDN, пока есть использующие эту
  сеть тома. В этой версии автоматического перевода на Internal нет.
- Агент читает актуальные адреса при reconcile; изменения NodeNetworkStatus
  сами по себе не запускают reconcile его DRBDResource. Немедленное применение
  смены IP не гарантируется.

<!--
Where: docs/USAGE_RU.md, «Создание ресурса ReplicatedStorageClass», примечание после абзаца о том, что контроллер создаёт StorageClass
Replaced by: примечание о том, что поля spec нельзя изменить после создания
-->

> Обратите внимание, что поля `topology`, `volumeAccess` и, после того как оно задано, `systemNetworkNames` ресурса ReplicatedStorageClass являются **неизменяемыми**: их изменение при обновлении отклоняется. Остальные поля `spec` у существующего ресурса можно изменить. Изменение `storage`, `zones`, `nodeLabelSelector` или `eligibleNodesPolicy` переносит класс в новый storage pool, а изменение `reclaimPolicy` заставляет модуль пересоздать StorageClass с новой политикой (см. «Обновление ресурса ReplicatedStorageClass»).

<!--
Where: docs/USAGE_RU.md, «Создание ресурса ReplicatedStorageClass», примечание после примера о проверке конфигурации перед созданием StorageClass
Replaced by: примечание о том, что при ошибках StorageClass не создаётся, а ошибка сохраняется в поле status
-->

> Конфигурация проверяется при создании и обновлении ресурса: некорректная отклоняется с ошибкой. StorageClass модуль создаёт сразу; можно ли уже создавать тома класса, показывает поле `status` ресурса ReplicatedStorageClass (см. «Проверка состояния ресурса ReplicatedStorageClass»).

<!--
Where: docs/USAGE_RU.md, отдельный раздел между «Создание ресурса ReplicatedStorageClass» и «Обновление ресурса ReplicatedStorageClass»
Replaced by: предложение о том, что контроллер поддерживает поле status в актуальном состоянии
-->

#### Проверка состояния ресурса ReplicatedStorageClass

`kubectl get replicatedstorageclass` показывает фазу каждого класса, его FTT/GMDR и число томов; `-o wide` добавляет topology, storage pool и сообщение, поясняющее фазу. Подробности — в conditions:

```shell
kubectl get replicatedstorageclass <RSC_NAME> -o jsonpath='{.status.phase}: {.status.message}{"\n"}{range .status.conditions[*]}{.type}={.status}/{.reason}: {.message}{"\n"}{end}'
```

Класс публикует конфигурацию, по которой строятся его тома (`status.configuration`). Конфигурация текущего `spec` **принята**, когда `status.configurationGeneration` равен `metadata.generation`. До этого новые тома класса её ждут, а существующие сохраняют прежнюю конфигурацию. Пока изменение не принято, сообщение `Ready` начинается с `Pending:` и называет, что именно ожидает применения.

| Фаза | `Ready` | Что означает | Что делать |
|---|---|---|---|
| `Ready` | `True/Ready` | Конфигурация принята, все тома с ней согласованы | Ничего |
| `RollingOut` | `True/Ready` | Тома приводятся в соответствие с конфигурацией (раскатка или тома, ещё не увидевшие конфигурацию) | Ждать; следить за `ConfigurationRolledOut` и `status.volumes` |
| `PartiallyAligned` | `True/Ready` | Часть томов не согласована, и все автоматические исправления для них отключены (`NewVolumesOnly`, `Manual`) | Включить раскатку или исправить тома вручную |
| `WaitingForStoragePool` | `False/WaitingForStoragePool` | Конфигурация текущего `spec` не принята: storage pool не готов. Новые тома ждут | Проверить `StoragePoolReady` и ReplicatedStoragePool: отсутствующие или некорректные LVMVolumeGroup, некорректный селектор узлов |
| `InsufficientNodes` | `False/InsufficientEligibleNodes` | Конфигурация текущего `spec` не принята: eligible nodes не удовлетворяют требованиям FTT/GMDR/topology. Новые тома ждут | Добавить узлы или ослабить `nodeLabelSelector`/`zones`/параметры избыточности |
| `Degraded` | `False/Degraded` | Storage pool не готов или его eligible nodes перестали удовлетворять требованиям после того, как конфигурация была принята. Принятая конфигурация продолжает действовать: новые тома получают её, существующие сохраняют, но реплики можно разместить только на узлах, которые storage pool ещё предоставляет, поэтому новый том может не сформироваться | Восстановить LVMVolumeGroup или узлы пула; причина — в `StoragePoolReady` или в сообщении. Если новый том не формируется, смотрите его conditions `Scheduled` и `MembershipLayoutConverged` |
| `InvalidConfiguration` | `False/InvalidConfiguration` | `spec` некорректен, например отсутствует `storage` | Исправить `spec` |
| `InvalidConfiguration` | `False/StorageClassConflict` | Имя класса уже занято StorageClass другого провижинера. Модуль не трогает этот StorageClass и не создаёт свой | Удалить чужой StorageClass или пересоздать класс под другим именем |
| `Terminating` | — | Класс удаляется; сообщение называет число томов, которые он ещё ждёт | Удалить оставшиеся тома |

Conditions класса:

- `Ready` — сводка, описанная выше;
- `StoragePoolReady` — condition `Ready` storage pool, в который класс переходит или на котором находится (`status.storagePoolName`), с его reason и сообщением;
- `ConfigurationRolledOut` — согласованы ли с классом все тома, берущие у него конфигурацию (см. «Обновление ресурса ReplicatedStorageClass»);
- `VolumesSatisfyEligibleNodes` — стоят ли реплики всех томов на eligible nodes; сообщение называет число томов, у которых это не так.

<!--
Where: docs/USAGE_RU.md, текст раздела «Обновление ресурса ReplicatedStorageClass» вместе с подразделами
Replaced by: абзац о том, что параметры StorageClass поменять невозможно
-->

Поля `topology`, `volumeAccess` и, после того как оно задано, `systemNetworkNames` неизменяемы: попытка их изменить отклоняется с ошибкой, называющей поле, и для изменения такого поля ресурс нужно пересоздать. Все остальные поля `spec` изменяемы:

- параметры репликации (`replication`, `failuresToTolerate`, `guaranteedMinimumDataRedundancy`) меняют целевой layout томов — например, при миграции r3→r2, описанной ниже;
- `storage`, `zones`, `nodeLabelSelector` и `eligibleNodesPolicy` образуют идентичность storage pool класса, поэтому их изменение переносит класс в новый storage pool;
- `reclaimPolicy` заставляет модуль пересоздать StorageClass с новой политикой;
- `volumeCleanup` тоже заставляет модуль пересоздать StorageClass; новый метод применяется только к томам, созданным после изменения (см. FAQ, «Как очищать данные удалённых томов?»);
- `configurationRolloutStrategy` и `eligibleNodesConflictResolutionStrategy` задают, как изменения доходят до существующих томов.

**Переход в новый storage pool.** Классы с одинаковыми `storage`, `zones`, `nodeLabelSelector`, `systemNetworkNames` и `eligibleNodesPolicy` используют один автоматически созданный storage pool (`status.storagePoolName`). После изменения любого из этих полей класс получает новый пул, а его тома следуют `configurationRolloutStrategy`: при `RollingUpdate` они раскатываются в новый пул (если их реплики уже стоят на подходящих узлах нового пула, данные не переносятся), при `NewVolumesOnly` существующие тома остаются в прежнем пуле, и в новом создаются только новые тома. Прежний пул освобождается, когда его перестаёт использовать последний том. После обновления модуля, изменившего способ построения имени пула, существующие классы остаются на своих пулах; класс переходит в новый пул при первом изменении **любого** поля `spec`, в том числе `reclaimPolicy` или `replication`, и его тома следуют `configurationRolloutStrategy`, как описано выше. Реплика, оставшаяся на узле или LVMVolumeGroup, которых нет в новом пуле, отражается как конфликт (`VolumesSatisfyEligibleNodes`); автоматическое разрешение таких конфликтов пока не реализовано (см. ограничения ниже), поэтому реплику нужно перенести вручную.

Пока новый пул не готов или его узлы не удовлетворяют требованиям FTT/GMDR/topology, изменение не принято: новые тома класса ждут его, а существующие сохраняют прежнюю конфигурацию (см. «Проверка состояния ресурса ReplicatedStorageClass»).

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

**Требования.** Для layout `2D+1TB` кроме двух diskful-узлов нужен узел под tie-breaker: не менее 3 узлов для топологии `Ignored`, ровно 3 зоны для `TransZonal` (таблицу числа зон см. в описании поля `zones` CRD ReplicatedStorageClass) либо не менее 3 узлов в зоне тома для `Zonal`. Это те же требования, что и для `3D`, поэтому миграция r3→r2 их не повышает.

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

**Рост класса (r2→r3).** Изменение `replication` с `Availability` на `ConsistencyAndAvailability` тоже не требует ручных шагов. Каждый том класса сам проходит путь `2D+1TB → 3D+1TB → 3D`: control plane создаёт третью копию, дожидается её синхронизации и только потом удаляет tie-breaker, который в `r3` не нужен, — именно в таком порядке, чтобы том ни в один момент не остался с двумя копиями данных без свидетеля. `MembershipLayoutConverged` всё это время называет текущий шаг (`creating diskful replica`, `waiting for a free resync slot`, `waiting for the diskful copies to synchronize before removing a surplus tie-breaker`, `tie-breaker ... is terminating`), а алерт `D8ReplicatedVolumeLayoutDegraded` молчит: он срабатывает только на `TransitionUnsupported` и `CannotConverge`. Однако пока третья копия не синхронизирована, избыточность тома ниже новой целевой (`Resilient=False/Degraded`), поэтому если рост занимает больше 30 минут (большой том или долгое ожидание свободного слота ресинхронизации), для этого тома срабатывает тикет `D8ReplicatedVolumeNotResilient`. Он резолвится сам после синхронизации копии, и одновременно в таком состоянии не больше `maxParallel` томов класса: тома, ещё ждущие слота rollout, сохраняют старую конфигурацию и остаются `Resilient`.

Слот раскатки том держит до самого конца — включая ресинк, — поэтому `maxParallel` ограничивает число томов класса, одновременно находящихся в середине роста. Если третью копию некуда разместить, том репортит `False/CannotConverge` со сводкой от scheduler'а и сохраняет tie-breaker: неудача размещения никогда не стоит тому отказоустойчивости.

**Ограничения.**

- Откат правки, пока том ещё мигрирует, не отменяет уже запущенный перевод реплики в tie-breaker. В зависимости от момента отката том останется либо в раскладке `2D+1TB` при желаемой `3D` — это ровно тот самый рост, описанный выше, и том сойдётся сам, — либо с репликой, у которой `spec.type` застрял в значении `TieBreaker`, тогда как раскладка по-прежнему `3D` (`MembershipLayoutConverged=False/Converging`, имя реплики указано в сообщении condition). Второй случай сам в `Converged` уже не вернётся; данные не теряются, а реплику нужно восстановить одним патчем: вернуть `spec.type` в `Diskful` и одновременно вернуть поля backing volume (`spec.lvmVolumeGroupName`, а для thin-пула ещё и `spec.lvmVolumeGroupThinPoolName`), взяв значения из `status.datamesh.members` тома.
- `eligibleNodesConflictResolutionStrategy: RollingRepair` (значение по умолчанию) принимается, но не реализован: реплики с узлов, переставших быть eligible, никто не переносит, а `rollingRepair.maxParallel` игнорируется. Такие конфликты отражаются в condition `VolumesSatisfyEligibleNodes` и в `status.volumes.inConflictWithEligibleNodes`, а реплики нужно переносить вручную. `maxParallel` из конфигурационной раскатки выше — другой параметр, и он работает.

<!--
Where: docs/USAGE_RU.md, отдельный раздел между «Обновление ресурса ReplicatedStorageClass» и «Удаление ресурса ReplicatedStorageClass»
Replaced by: nothing
-->

#### Восстановление потерянной реплики

Том, потерявший diskful-реплику — узел удалили, реплику force-removed, член ушёл из datamesh по любой другой причине, — восстанавливается сам. Control plane создаёт замещающий `ReplicatedVolumeReplica`, scheduler его размещает, datamesh включает его в состав, данные ресинхронизируются. Ручных шагов нет, и порядок один и тот же независимо от того, что именно убрало члена.

Пока идёт восстановление, том показывает прогресс в `MembershipLayoutConverged`, например `False/Converging: creating diskful replica: have 2D, want 3D`. Алерт `D8ReplicatedVolumeLayoutDegraded` при этом молчит: он срабатывает только на `TransitionUnsupported` и `CannotConverge`. А `D8ReplicatedVolumeNotResilient` срабатывает, если восстановление занимает больше 30 минут: пока новая копия не синхронизирована, том не переживёт следующий отказ.

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

DRBD-алерты (`D8DrbdPeerDeviceIsOutOfSync`, `D8DrbdDeviceHasNoQuorum`, `D8DrbdResourceIOSuspended`, `D8DrbdDeviceIsNotConnected`) несут две полезные метки:

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

Когда оба конца соединения в `UpToDate`, но остаточные байты `out-of-sync` «заморожены», DRBD не пересинхронизирует их сам (совпадающие generation UUID означают, что пересинхронизация не запускается). При переподключении пира DRBD заново сравнивает текущие generation UUID обоих концов. Новый текущий UUID создаёт только Primary, который пишет, пока не подключён к пиру, поэтому исход зависит от того, где Primary:

- Primary нет ни на одном конце этого соединения (например, это diskless-реплика (реплика без диска) на другом узле) или он не писал, пока соединения не было, — UUID по-прежнему совпадают, и DRBD сбрасывает биты `out-of-sync` этого соединения, не передавая и не сравнивая данные, даже если в том за это время писали;
- Primary на одном из концов писал, пока соединения не было, — блоки `out-of-sync` пересинхронизируются по битовой карте (bitmap-based resync) вместе с блоками, записанными за это время. Пересинхронизация тем дольше, чем больше остаток: остаток на весь том означает полную пересинхронизацию, и другой конец остаётся `Inconsistent` до её конца;
- UUID различались ещё до переподключения (diskless-реплика показывает пира как `peer-disk:Consistent`, см. ниже) — DRBD по истории UUID решает, нужна ли пересинхронизация, или сообщает о split brain (расхождении копий, у которых нет общей истории), независимо от того, были ли записи.

Переподключение данные не проверяет: если блоки могут действительно различаться, вместо него проверьте их верификацией и перезеркальте одну копию (см. «Если остаётся небольшой остаток» ниже).

Переподключайте по одному соединению за раз и перед каждым переподключением проверяйте все реплики тома. Переподключение ненадолго разрывает соединение: если другая реплика уже в `Outdated`, `Inconsistent` или пересинхронизируется, том может потерять кворум и приостановить ввод-вывод. Так происходит, когда у узла `out-of-sync` сразу к нескольким пирам (по алерту на каждое соединение) и соединения переподключают одно за другим.

1. Проверьте все реплики тома. `<volume>` — имя PersistentVolume; DRBD-ресурс реплики на её узле называется `sdsrv-<имя DRBDResource>`, то есть `sdsrv-<volume>-<N>`:

   ```shell
   d8 k --as=system:sudouser get drbdr -l sds-replicated-volume.deckhouse.io/replicated-volume=<volume>
   ```

   У каждой реплики с диском в `DISKSTATE` должно быть `UpToDate`, у каждой реплики в `QUORUM` — `true`. На каждом узле из списка статус его DRBD-ресурса (см. «Просмотр состояния ресурса») не должен показывать диск в `Outdated` или `Inconsistent`, идущую пересинхронизацию (`replication:SyncSource` или `replication:SyncTarget`) и `quorum:no`. Если хоть одно условие не выполняется, ничего не переподключайте: дождитесь конца пересинхронизации или сначала разберитесь с отказавшей репликой.

2. Переподключите одно соединение и проверьте, что его `out-of-sync` обнулился:

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup disconnect <resource> <peer-node-id>
   # out-of-sync должен обнулиться:
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup status <resource> --statistics
   ```

3. Перед переподключением следующего соединения повторите шаг 1. Если переподключение запустило пересинхронизацию, дождитесь её конца. Если реплика остаётся в `Outdated` или `Inconsistent` или реплика теряет кворум, остановитесь и больше ничего не переподключайте; при потере кворума см. «Восстановление потерянного кворума» выше.

Diskless-реплика (реплика без диска) может показывать пира с диском как `peer-disk:Consistent` вместо `UpToDate`. DRBD так делает, когда текущее поколение данных пира отличается от того, с которым работает diskless-реплика; здесь это признак того же `out-of-sync`, а не отдельная авария.

Если остаётся небольшой остаток:

1. Запустите онлайн-верификацию этого соединения. Она читает оба диска и помечает различающиеся блоки как `out-of-sync`; она их не пересинхронизирует и не снимает биты совпадающих блоков, поэтому счётчик не уменьшается:

   ```shell
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $POD -c agent -- drbdsetup verify <resource> <peer-node-id> 0
   ```

   Когда соединение вернётся в `replication:Established`, смотрите результат в журнале ядра узла (`dmesg` на узле): строки `Out of sync: start=..., size=...` перечисляют блоки, которые верификация нашла различающимися. Итоговая строка `Online verify found N blocks out of sync` считает всю битовую карту соединения вместе со старым остатком, поэтому их не различает. Если строк `Out of sync:` нет, копии идентичны, а остаток — устаревший учёт, а не расхождение. Не переподключайте соединение после верификации, нашедшей различающиеся блоки: при совпадающих UUID переподключение сбросит биты, поставленные верификацией, без пересинхронизации.

2. Если верификация нашла различающиеся блоки, полностью перезеркальте одну копию. Никогда не инвалидируйте диск Primary: инвалидируйте тот конец соединения, который не Primary. Если ни один конец не Primary, понять, какая копия верна, нельзя: остановитесь и обратитесь в техподдержку. Перед инвалидацией проверьте все реплики тома, как в шаге 1 переподключения выше: все остальные реплики с диском должны быть в `UpToDate` и подключены. Инвалидированная копия станет `Inconsistent` и выполнит полную пересинхронизацию от актуальной реплики. Кворум на время resync держат остальные `UpToDate`-реплики; при двух репликах с диском (`2D+1TB`) у тома до конца пересинхронизации нет второй актуальной копии. На узле копии, которую нужно инвалидировать:

   ```shell
   TARGET_POD=$(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<target-node> -l app=agent -o name)
   MIN=$(d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $TARGET_POD -c agent -- drbdsetup status <target-resource> --verbose | grep -oE 'minor:[0-9]+' | head -1 | cut -d: -f2)
   d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $TARGET_POD -c agent -- drbdsetup invalidate $MIN
   ```

**Фантомный счётчик (phantom counter).** Если после полного перезеркалирования Primary всё ещё показывает крошечный `out-of-sync` к пиру, чьи данные только что были побитово перечитаны, — это остаточный артефакт учёта на стороне Primary, а не реальное расхождение: данные на всех репликах идентичны. Переподключение, верификация и перезеркалирование его не снимут — он сбрасывается только при деструктивном действии (failover — переключение роли — или перезагрузке узла-Primary). Для рабочего тома его безопасно оставить до ближайшего окна обслуживания.

<!--
Where: docs/FAQ_RU.md, ответ на «Я не могу обновить поля в spec у ресурса ReplicatedStorageClass. Это ожидаемое поведение?»
Replaced by: ответ о том, что изменение spec не поддерживается
-->

Только для нескольких полей. Поля `topology`, `volumeAccess` и, после того как оно задано, `systemNetworkNames` неизменяемы, и их обновление отклоняется с ошибкой, называющей поле, — чтобы их изменить, ресурс нужно пересоздать. Все остальные поля **можно** менять у существующего ресурса. Изменением `replication` (или `failuresToTolerate`/`guaranteedMinimumDataRedundancy`) выполняется миграция томов между layout'ами — например с `ConsistencyAndAvailability` (`3D`) на `Availability` (`2D+1TB`); изменение `storage`, `zones`, `nodeLabelSelector` или `eligibleNodesPolicy` переносит класс в новый storage pool; изменение `reclaimPolicy` пересоздаёт StorageClass с новой политикой; изменение `volumeCleanup` тоже пересоздаёт StorageClass и действует только на тома, созданные после изменения. См. раздел «Обновление ресурса ReplicatedStorageClass» в руководстве по использованию.

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

<!--
Where: docs/FAQ_RU.md, текст раздела «Как получить информацию об используемом пространстве?»
Replaced by: ответ, отсылающий к дашборду «LINSTOR/DRBD» и `linstor storage-pool list`
-->

Откройте в Grafana «Dashboards» → «Storage» → «SDS Replicated Volume» и разверните строку «Capacity by ReplicatedStorageClass». Классы выбираются в списке «Storage Class» вверху дашборда, и каждая панель строки показывает каждый выбранный класс отдельно:

- «Raw Capacity», «Raw Free» и «Raw Used» показывают ёмкость пула хранения класса без учёта репликации. Для толстого пула (`LVM`) это размер и свободное место его групп томов (Volume Group). Для тонкого пула (`LVMThin`) это лимит выделения его thin-пулов, то есть размер thin-пула, умноженный на `allocationLimit` LVMVolumeGroup (по умолчанию 150 %), а свободное место — этот лимит за вычетом места, уже выделенного тонким томам.
- «Usable Capacity» и «Usable Free» показывают те же значения, делённые на число реплик класса («Replicas»): `failuresToTolerate + guaranteedMinimumDataRedundancy + 1`, то есть 1 для `None`, 2 для `Availability` и `Consistency` и 3 для `ConsistencyAndAvailability`. Это объём данных томов, который поместится в класс. Оценка сверху: реплики одного тома размещаются на разных узлах, и каждая целиком помещается в одну группу томов, поэтому при неравномерно заполненных узлах фактически поместится меньше.
- «Thin Pools: Allocation and Data by Storage Class» показывает для thin-пулов выделенное место в сравнении с лимитом выделения и фактически записанные данные в сравнении с физическим размером thin-пулов. Если записанные данные приближаются к физическому размеру, место в thin-пулах скоро закончится, сколько бы ни разрешал лимит выделения.
- «Capacity by Node / LVMVolumeGroup» перечисляет все LVMVolumeGroup (и thin-пулы) пулов хранения выбранных классов: узел, ёмкость, свободное место и занятую долю.

Классы с одинаковыми настройками хранения используют один пул хранения (`status.storagePoolName`) и делят его ёмкость: значения без учёта репликации у них совпадают, а место, занятое одним классом, другим уже недоступно.

Ёмкость LVMVolumeGroup берётся из метрик модуля `sds-node-configurator` (`sds_node_configurator_lvg_*`). Контроллер `sds-replicated-volume` экспортирует, какие LVMVolumeGroup входят в пул хранения и сколько реплик у класса:

- `sds_rsp_lvg_info{storage_pool, lvg_name, thin_pool}` — значение 1 для каждой LVMVolumeGroup (и thin-пула) из `spec.lvmVolumeGroups` ReplicatedStoragePool; у пулов `LVM` метка `thin_pool` пустая.
- `sds_rsc_replicas{storage_class, storage_pool}` — число diskful-реплик (реплик с данными) у томов класса и пул хранения, в котором они размещаются; оба значения берутся из принятой конфигурации класса (`status.configuration`).

Например, свободное место для данных томов у классов с толстыми пулами:

```text
sum by (storage_pool) (
  max by (lvg_name) (sds_node_configurator_lvg_vg_free_bytes)
  * on (lvg_name) group_right max by (storage_pool, lvg_name) (sds_rsp_lvg_info{thin_pool=""})
)
/ on (storage_pool) group_right max by (storage_class, storage_pool) (sds_rsc_replicas)
```

<!--
Where: docs/FAQ_RU.md, отдельный раздел после «Сколько места тома резервируется под суперпользователя?»
Replaced by: ничего
-->

## Как очищать данные удалённых томов?

По умолчанию данные удалённого тома не очищаются: место его логических томов возвращается в группу томов (или thin-пул) как есть, и ничто не гарантирует, что созданный позже на этом месте том не сможет их прочитать. Чтобы данные перезаписывались до освобождения места, задайте метод в поле `spec.volumeCleanup` ресурса `ReplicatedStorageClass`:

```yaml
apiVersion: storage.deckhouse.io/v1alpha1
kind: ReplicatedStorageClass
metadata:
  name: <name>
spec:
  volumeCleanup: RandomFillSinglePass
  # ...
```

Методы те же, что у модуля [sds-node-configurator](https://deckhouse.ru/modules/sds-node-configurator/), который и выполняет очистку:

- `RandomFillSinglePass` — логический том перезаписывается случайными данными один раз;
- `RandomFillThreePass` — логический том перезаписывается случайными данными три раза;
- `Discard` — все блоки логического тома отмечаются как свободные с помощью системного вызова `discard`. Только для thick-томов (`storage.type: LVM`) на твердотельных накопителях: класс с `Discard` на thin-хранилище отклоняется.

Оба метода `RandomFill` изнашивают твердотельные накопители и для них не рекомендуются. Для thin-томов они перезаписывают только пространство, занятое на момент удаления.

Что нужно учитывать:

- **Редакции.** Очистка доступна только в редакциях Enterprise Edition и Certified Security Edition Pro. В остальных редакциях ресурс `ReplicatedStorageClass` с `volumeCleanup` отклоняется валидирующим вебхуком.
- **Метод фиксируется при создании тома.** Он записывается в поле `spec.volumeCleanup` ресурса `ReplicatedVolume`, и изменить его там нельзя. Изменение поля класса приводит к пересозданию StorageClass и затрагивает только тома, созданные после изменения; существующие тома сохраняют свой метод. Тома, созданные до того, как поле было задано, не очищаются. Узнать метод тома:

  ```shell
  d8 k get replicatedvolume <pv-name> -o jsonpath='{.spec.volumeCleanup}{"\n"}'
  ```

- **Очищается каждая копия, и не только при удалении тома.** Метод действует для логических томов всех реплик тома, в том числе созданных позже, и они очищаются всякий раз, когда удаляются: при удалении тома, а также когда реплика переезжает на другой узел или в другое хранилище, становится бездисковой или удаляется. Очистка не начинается, пока логический том использует DRBD.
- **Время.** Перезапись занимает время, пропорциональное размеру тома, и выполняется для каждой реплики (для `RandomFillThreePass` — трижды). Удалённый том и его PersistentVolume исчезают только после очистки всех его логических томов; пока идёт очистка, ресурс `LVMLogicalVolume` находится в фазе `Cleaning`:

  ```shell
  d8 k get lvmlogicalvolume -l sds-replicated-volume.deckhouse.io/replicated-volume=<pv-name>
  ```

  Место тома освобождается только после очистки, поэтому свободное место в пуле возвращается позже, чем без неё.
