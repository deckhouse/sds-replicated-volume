# E2E Тестирование Мигратора Linstor (sds-replicated-volume)

Данные тесты предназначены **исключительно для ручного запуска** и не интегрированы в CI.
Из-за специфики миграции control plane (переключение старых компонентов на новые), каждый сценарий требует чистого состояния кластера.

## Подход к тестированию

Тесты разделены на независимые сценарии. **ВАЖНО:** Нельзя запускать все тесты разом! Верхнеуровневые блоки `Describe` в Ginkgo выполняются последовательно на одном и том же кластере. Если первая миграция уже прошла, вторая упадет, так как кластер уже будет находиться на новом control plane.

Жизненный цикл тестирования выглядит так:
1. Запуск **одного** конкретного сценария через `make test-scenario` (нужен `LABEL`, см. ниже).
2. Ручной запуск скрипта очистки (`scripts/cleanup.sh`) **непосредственно на мастер-узле тестового кластера**. (Этот скрипт не поддерживает запуск локально с машины разработчика).
3. Запуск следующего сценария на очищенном кластере.

### Реализуемые сценарии (Describe блоки):
* **`All RVs are created with ConfigurationMode: Auto`** (Label: `"Auto"`): Базовый флоу, когда все ресурсы Linstor имеют связанные RSC до начала миграции.
* **`All RVs are created with ConfigurationMode: Manual`** (Label: `"Manual"`): Эмуляция ситуации, когда все RSC были удалены до начала миграции.
* **`RVs are created with mixed ConfigurationMode (Auto and Manual)`** (Label: `"Mixed"`): Гибридный сценарий, где часть ресурсов имеет RSC, а часть — нет.

## Настройка окружения (Переменные)

Для запуска тестов необходимо подготовить файл с экспортами переменных окружения (например, `test_exports.sh`).

### Шаг 1: Создание нового тестового кластера (Первый запуск)
Для запуска первого сценария кластер необходимо создать с нуля.

```bash
# --- Постоянные переменные
export TEST_CLUSTER_NAMESPACE='e2e-linstor-migrator'
export TEST_CLUSTER_STORAGE_CLASS='huawei-storage-stable'
export KUBE_CONFIG_PATH=$HOME/.kube/config-san

# Если тест прерывается по ctrl+c, то следующий запуск должен увидеть эту переменную
export TEST_CLUSTER_FORCE_LOCK_RELEASE='true'

# PR, на который переключится новый control plane
export TEST_MIGRATOR_MPO_IMAGE_TAG=pr631

# Больше логов во время запуска тестов
export TEST_DEBUG=1
export TEST_CLUSTER_VIRTUAL_MACHINE_CLASS_NAME=e2e-host

export DKP_LICENSE_KEY=xxxxxxx
export REGISTRY_DOCKER_CFG=<base64>

# --- Создание кластера
export TEST_CLUSTER_CREATE_MODE='alwaysCreateNew'
export TEST_CLUSTER_CLEANUP='false'

# Базовый кластер
export SSH_HOST=172.17.1.67
export SSH_USER=tfadm
export SSH_PRIVATE_KEY=$HOME/.ssh/flant/id_rsa
export SSH_PUBLIC_KEY=$HOME/.ssh/flant/id_rsa.pub
# Задается только если ключ имеет пароль, иначе не задается
export SSH_PASSPHRASE=xxxxxxx
```

### Шаг 2: Использование существующего кластера (Последующие запуски)
После того как первый тест отработал, **обязательно очистите тестовый кластер** (см. раздел "Очистка"). 
Затем измените переменные для запуска следующего теста на уже существующем кластере:

```bash
# ... (постоянные переменные остаются теми же) ...

# --- Использование созданного ранее кластера
export TEST_CLUSTER_CREATE_MODE='alwaysUseExisting'

# Базовый кластер (jump host)
export SSH_JUMP_HOST=172.17.1.67
export SSH_JUMP_USER=tfadm

# Тестовый (ранее созданный) кластер
export SSH_HOST=10.211.1.6
export SSH_USER=cloud

# ID ранее сохраненного состояния (только если вы хотите запустить пост-проверку без повторной миграции)
# Например: make test-scenario-it LABEL="Auto" FOCUS="RV resource checks"
# Не задавать, если не знаете, зачем это нужно!
# export TEST_PREVIOUS_RUNID=5a6eaefb
```

## Запуск тестов

Из-за необходимости изолировать сценарии, обычная команда `make test` удалена.
Используйте `make test-scenario` и `make test-scenario-it`:

- **`LABEL`** — Ginkgo-метка сценария (`Auto`, `Manual`, `Mixed`).
- **`FOCUS`** — аргумент `-ginkgo.focus` (регулярное выражение по цепочке имён `Describe` / `It` в Ginkgo).

Запуск всего сценария по метке (миграция и все вложенные проверки):
```bash
make test-scenario LABEL="Auto"
```

Запуск конкретной проверки внутри сценария:
```bash
make test-scenario-it LABEL="Auto" FOCUS="RV resource checks"
```

## Очистка

После успешного или неудачного завершения сценария, перед запуском следующего теста необходимо полностью удалить тестовые ресурсы из кластера.

**Внимание:** Скрипт очистки нельзя запускать с машины разработчика!
1. Скопируйте скрипт на мастер-узел тестового кластера: `scp scripts/cleanup.sh <USER>@<CLUSTER_IP>:/tmp/`
2. Зайдите по SSH на тестовый кластер.
3. Запустите скрипт: `bash /tmp/cleanup.sh`
