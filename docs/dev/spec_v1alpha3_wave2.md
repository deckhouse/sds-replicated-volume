- [status.conditions - часть клиентского api](#statusconditions---часть-клиентского-api)
- [Actual поля](#actual-поля)
- [Акторы приложения: `agent`](#акторы-приложения-agent)
  - [`drbd-config-controller`](#drbd-config-controller)
  - [`drbd-resize-controller`](#drbd-resize-controller)
  - [`drbd-primary-controller`](#drbd-primary-controller)
  - [`rvr-drbd-status-controller`](#rvr-drbd-status-controller)
  - [`rvr-status-config-address-controller`](#rvr-status-config-address-controller)
- [Акторы приложения: `controller`](#акторы-приложения-controller)
  - [`rvr-diskful-count-controller`](#rvr-diskful-count-controller)
  - [`rvr-scheduling-controller`](#rvr-scheduling-controller)
  - [`rvr-status-config-node-id-controller`](#rvr-status-config-node-id-controller)
  - [`rvr-status-config-peers-controller`](#rvr-status-config-peers-controller)
  - [`rv-status-config-device-minor-controller`](#rv-status-config-device-minor-controller)
  - [`rvr-tie-breaker-count-controller`](#rvr-tie-breaker-count-controller)
  - [`rvr-access-count-controller`](#rvr-access-count-controller)
  - [`rv-publish-controller`](#rv-publish-controller)
  - [`rvr-volume-controller`](#rvr-volume-controller)
  - [`rvr-quorum-and-publish-constrained-release-controller`](#rvr-quorum-and-publish-constrained-release-controller)
  - [`rvr-owner-reference-controller`](#rvr-owner-reference-controller)
  - [`rv-status-config-quorum-controller`](#rv-status-config-quorum-controller)
  - [`rv-status-config-shared-secret-controller`](#rv-status-config-shared-secret-controller)
  - [`rvr-missing-node-controller`](#rvr-missing-node-controller)
  - [`rvr-node-cordon-controller`](#rvr-node-cordon-controller)
  - [`rvr-status-conditions-controller`](#rvr-status-conditions-controller)
  - [`llv-owner-reference-controller`](#llv-owner-reference-controller)
  - [`rv-status-conditions-controller`](#rv-status-conditions-controller)
  - [`rv-gc-controller`](#rv-gc-controller)
  - [`tie-breaker-removal-controller`](#tie-breaker-removal-controller)

## status.conditions - часть клиентского api
Для наших нужд используем поля в `status`

## Actual поля
Для контроля состояния, там где невозможно использовать generation (при обновлении конфигов в status),
мы вводим дополнительные поля `actual*`.
- shared-secret-controller

# Акторы приложения: `agent`

## `drbd-config-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `drbd-resize-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `drbd-primary-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-drbd-status-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-config-address-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

# Акторы приложения: `controller`

## `rvr-diskful-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-scheduling-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-config-node-id-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-config-peers-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-device-minor-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-tie-breaker-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-access-count-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-publish-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-volume-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-quorum-and-publish-constrained-release-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-owner-reference-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-quorum-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-config-shared-secret-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-missing-node-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-node-cordon-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rvr-status-conditions-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `llv-owner-reference-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-status-conditions-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `rv-gc-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.

## `tie-breaker-removal-controller`

### Уточнение
Пока на rv нет нашего финализатора "[sds-replicated-volume.storage.deckhouse.io/controller](spec_v1alpha3.md#финализаторы-ресурсов)", rv не обрабатываем.
