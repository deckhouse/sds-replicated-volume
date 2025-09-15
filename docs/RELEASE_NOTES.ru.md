---
title: "Релизы"
---

## v0.8.6

* Добавлены файлы release notes
* Перевод хуков в модуле с python на golang
* Документация улучшена

## v0.8.5

* Добавлены дополнительные монтирования для поддержки containerd v2

## v0.8.4

* Добавлена информация о необходимости snapshot-controller для работы модуля

## v0.8.3

* Правки в документацию
* Добавлена зависимость от snapshot-controller

## v0.8.2

* Правки в хук обновления сертификатов
* Удаление устаревших хуков миграции

## v0.8.1

* Правки в документацию (добавлена инструкция по расширению ReplicatedStoragePool на новый узел кластера)

## v0.8.0

* Рефакторинг модуля
* Правки в документацию
* Правки для поддержки снимков томов

## v0.7.4

* Если топология позволяет, контроллером убирается аннотация для StorageClass, запрещающая заказ RWX томов

## v0.7.3

* Рефакторинг модуля
* Исправлен podAntiAffinity для sds-replicated-volume-controller

## v0.7.2

* Изменения в хуки для корректного процесса ручного обновления сертификатов
* Поправлена группировка алерт D8NodeHighUnknownMemoryUsage

## v0.7.1

* Добавлен патч в CSI для полноценной поддержки топологий, указанных в ReplicatedStorageClass (могли игнорироваться)
* Добавлен алерт D8NodeHighUnknownMemoryUsage для отлова случаев утечки памяти в DRBD (о проблемах сообщать в team storage)

## v0.6.0

* Обновление DRBD до версии v9.2.12, решающее ряд проблем (в частности улучшающее стабильность DRBD diskless реплик)

## v0.5.1

* Исправлен алерт на некорректное количество реплик ресурсов
* Исправления в расписание job по снятию резервных копий БД Linstor

## v0.5.0

* Множественные небольшие правки в templates, monitoring alerts и документацию
* Переход с linstor scheduler-extender на внутренние механизмы самого Deckhouse (KubeSchedulerWebhookConfiguration)
* Перевод образов на distroless
* Поправлен и дополнен скрипт для вывода drbd ресурсов с ноды

## v0.4.3

* Технический релиз. Правки и дополнения в скрипт по вытеснению ресурсов с ноды evict.sh, правки в templates и документацию

## v0.4.1

* В скрипте очистки ноды от DRBD-ресурсов evict.sh теперь учитывается установленный параметр AutoplaceTarget, перемещаемые реплики не будут перемещены на ноды со значением AutoplaceTarget равном false

## v0.4.0

* Обновлены библиотеки golang API для поддержки sds-node-configurator v0.4.0
* Множественные правки в контроллеры и документации

## v0.3.7

* {'Прыгаем через версию, потому применятся все правки и изменения от версий': '0.3.5'}

## v0.3.5

* Множественные исправления и улучшения в evict.sh и replicas_managers.sh (также, они теперь автоматически устанавливаются в /opt/deckhouse/sbin)
* DRBD теперь корректно собирается на ALT Linux и с ядром Linux 6.5+
* Добавлены правила anti-affinity для pod'ов контроллеров
* Множественные исправления в дашборде и алертах
* Параметр isDefault удален; используйте стандартную аннотацию k8s вместо него
* Добавлены liveness и readiness checks для контроллеров
* Резервное копирование переключено на выделенный CR вместо использования секретов в namespace модуля
* Запрещено создание пулов на эфемерных узлах
* Множественные исправления документации
* CSI endpoint мигрирует с linstor.csi.linbit.com на replicated.csi.deckhouse.io

## v0.2.9

* Add DRBD ports range settings
* Fix path in liveness-satellite
* Actual typo lvmVolumeGroups and thinPoolName in examples
* Add a check for a Linstor node's AutoplaceTarget property
* Changed lvmvolumegroups to lvmVolumeGroups in russian docs
* Fix linstor satellite VPA

## v0.2.8

* Add check if /etc/modules file exists
* Add liveness probe for linstor-node
* Add age field
* {'Patch CSI to 98544cadb6d111d27a86a11ec07de91b99704b82': 'Prevent Node Reboots on Volume Deletion'}

## v0.1.11

* Фикс enabled скрипта, модуль не будет выключен, если из кластера пропадет модуль sds-node-configurator
