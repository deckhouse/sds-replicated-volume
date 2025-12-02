# Controller Style Guide

## Правила для написания контроллеров в проекте

Этот документ описывает стандарты и best practices для создания контроллеров в проекте. Следуйте этим правилам для обеспечения консистентности и соответствия review requirements.

---

## 1. Использовать стандартный `reconcile.Reconciler`

**⚠️ Не используйте без веской причины:**
- `reconcile.TypedReconciler[Request]` с кастомными типами запросов
- Кастомные `Request` интерфейсы и типы
- Типизированные очереди `workqueue.TypedRateLimitingInterface[TReq]`

**✅ Используйте по умолчанию:**
- Стандартный `reconcile.Reconciler` с `reconcile.Request`
- `reconcile.Request` содержит `NamespacedName` (Namespace и Name)
- Стандартные очереди `workqueue.RateLimitingInterface`

**Обоснование:** Стандартный reconciler достаточен для 99% задач. Типизированные версии добавляют сложность без существенных преимуществ. Используйте их только если есть реальная необходимость (например, если стандартный подход не позволяет решить задачу).

**Пример:**
```go
var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	// Использовать req.NamespacedName для получения ресурса
	obj := &v1alpha3.SomeResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, err
	}
	// ...
}
```

---

## 2. Использовать `.For()` для основного ресурса

**⚠️ Не используйте без веской причины:**
- Только `.Watches()` без `.For()`
- `builder.TypedControllerManagedBy[TReq](mgr)`

**✅ Используйте по умолчанию:**
- `builder.ControllerManagedBy(mgr)` (стандартный builder)
- `.For(&ResourceType{})` перед `.Watches()` для указания основного ресурса
- Это стандартный паттерн controller-runtime

**Обоснование:** `.For()` явно указывает основной ресурс, который контроллер контролирует. Это делает код более читаемым и соответствует стандартам controller-runtime. Используйте только `.Watches()` без `.For()` только если контроллер не имеет основного ресурса (редкий случай).

**Пример:**
```go
err := builder.ControllerManagedBy(mgr).
	Named("controller_name").
	For(&v1alpha3.ReplicatedVolumeReplica{}).
	Complete(rec)
```

---

## 3. Не использовать фильтры событий (WithEventFilter)

**❌ Не используйте:**
- `WithEventFilter(predicate.Funcs{...})` для фильтрации событий на уровне контроллера
- Фильтрация событий в обработчиках `.Watches()`

**✅ Используйте по умолчанию:**
- Обрабатывать все события (CREATE/UPDATE/DELETE/Generic) через `.For()`
- Фильтрацию делать в методе `Reconcile` через early return, если обработка не нужна
- Идемпотентные проверки в начале `Reconcile` для предотвращения лишних операций

**Обоснование:** Фильтры событий могут генерировать сложные для поиска ошибки. Если фильтр пропустит нужное событие или неправильно отфильтрует, это может быть сложно отладить. Лучше обрабатывать все события и делать проверки в `Reconcile` - это более явно и проще для отладки.

**Пример правильного подхода:**
```go
// controller.go - БЕЗ фильтров
err := builder.ControllerManagedBy(mgr).
	Named("controller_name").
	For(&v1alpha3.ReplicatedVolumeReplica{}).
	Complete(rec)

// reconciler.go - фильтрация через early return
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.LogAlt.WithName("Reconcile").WithValues("req", req)
	
	obj := &v1alpha3.SomeResource{}
	if err := r.Cl.Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, err
	}
	
	// Идемпотентная проверка - если уже обработано, выходим
	if obj.Status != nil && obj.Status.SomeField != nil {
		log.V(1).Info("already processed", "field", *obj.Status.SomeField)
		return reconcile.Result{}, nil
	}
	
	// Обработка...
}
```

**Примечание:** На данный момент (по решению review) фильтры событий не используются в проекте. Если в будущем возникнет необходимость в фильтрации, это должно быть обсуждено с командой.

---

## 4. Использование handlers для маппинга событий

**✅ Используйте в первую очередь:**
- `.For(&ResourceType{})` - для основного ресурса, который контроллер управляет
- `handler.EnqueueRequestForOwner` - когда нужно реагировать на изменения дочерних ресурсов, которые имеют owner reference на основной ресурс

**⚠️ Используйте только если не хватает функционала выше:**
- `handler.EnqueueRequestsFromMapFunc` - когда нужно маппить события одного ресурса на reconcile другого ресурса, но нет owner reference
  - Пример: меняется ConfigMap, и надо реконсайлить поды с этим ConfigMap, при этом под owner'ом ConfigMap стать не может

**❌ Не используйте на данном этапе:**
- `handler.TypedFuncs` с кастомными типами
- Другие сложные handlers - они для оптимизации производительности, используйте только после обсуждения с командой или если нет других вариантов. 

**Обоснование:** `.For()` и `EnqueueRequestForOwner` покрывают 99% случаев. `EnqueueRequestsFromMapFunc` используется только когда нет owner reference, но нужен маппинг событий. Остальные handlers добавляют сложность и используются только для оптимизации производительности.

**Пример:**
```go
Watches(
	&v1alpha3.SomeChildResource{},
	handler.EnqueueRequestForOwner(
		mgr.GetScheme(),
		mgr.GetRESTMapper(),
		&v1alpha3.SomeResource{},
	),
)
```

**Важно:** 
- Если используются кастомные Watch handlers (например, `handler.EnqueueRequestsFromMapFunc` с кастомной логикой), выносите их в отдельный файл `handler.go` в директории контроллера
- Если вспомогательные функции для handlers находятся в `controller.go` (например, функции для predicate), тесты для них должны быть в `controller_test.go`, а не в отдельном `handler_test.go`
- **Принцип:** Тесты должны быть рядом с кодом, который они тестируют. Если функция в `controller.go` - тесты в `controller_test.go`. Если функция в `handler.go` - тесты в `handler_test.go`
- Не создавайте отдельные тестовые файлы без соответствующих исходных файлов

**Пример структуры:**
```go
// handler.go
package mycontroller

import (
	"context"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func mapRVRToRV(_ context.Context, obj client.Object) []reconcile.Request {
	rvr, ok := obj.(*v1alpha3.ReplicatedVolumeReplica)
	if !ok {
		return nil
	}
	// Кастомная логика маппинга
	return []reconcile.Request{
		{NamespacedName: client.ObjectKey{Name: rvr.Spec.ReplicatedVolumeName}},
	}
}

// handler_test.go
package mycontroller_test

import (
	"testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	// ... тесты для handlers
)
```

---

## 5. Использовать structured logger

**⚠️ Не используйте без веской причины:**
- `slog.Logger` напрямую в методах Reconcile
- Простые логи без контекста
- `r.log.Info(...)` без структурирования

**✅ Используйте по умолчанию:**
- `logr.Logger` с `.WithName()` и `.WithValues()` для структурированного логирования
- Создавать logger в начале метода `Reconcile` с контекстом запроса
- Использовать уровни логирования (`V(1)` для debug)

**Обоснование:** Structured logging улучшает трейсинг, отладку и мониторинг. Контекст запроса помогает отслеживать обработку конкретных ресурсов. Используйте `slog.Logger` напрямую только если есть специфические требования к логированию.

**Пример:**
```go
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")
	
	// ...
	
	log.Info("completed", "result", "success", "nodeID", nodeID)
	log.V(1).Info("debug info") // для debug уровня
	log.Error(err, "failed to process")
}
```

**Уровни логирования:**
- `log.Info()` - обычная информация (всегда видна)
- `log.V(1).Info()` - debug информация (требует увеличения verbosity)
- `log.Error()` - ошибки (всегда видна)

---

## 6. Упрощенная структура Reconciler

**⚠️ Не используйте без веской причины:**
- Неиспользуемые поля (`rdr client.Reader`, `sch *runtime.Scheme`)
- Экспортированные поля структуры (нарушают инкапсуляцию)

**✅ Используйте по умолчанию:**
- Минимальная структура с только необходимыми полями: `cl`, `log` (только `logr.Logger`)
- Приватные поля (с маленькой буквы) для инкапсуляции
- Функция-конструктор `NewReconciler` для создания экземпляра (используется в тестах)
- Проверка интерфейса через `var _ reconcile.Reconciler = (*Reconciler)(nil)` (nil pointer, не выделяет память)

**Обоснование:** Приватные поля обеспечивают инкапсуляцию и соответствуют стилю проекта. Использование только `logr.Logger` упрощает код и соответствует стилю peers-controller. Конструктор `NewReconciler` позволяет создавать экземпляры в тестах. Использование nil pointer для проверки интерфейса более эффективно (не выделяет память).

**Пример:**
```go
// ✅ ПРАВИЛЬНО - приватные поля с конструктором, только logr.Logger
type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
// This is primarily used for testing, as fields are private.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

// В controller.go - прямое создание (тот же пакет, можно обращаться к приватным полям)
r := &Reconciler{
	cl:  mgr.GetClient(),
	log: mgr.GetLogger().WithName(ControllerName).WithName("Reconciler"),
}

// В тестах - использование NewReconciler (другой пакет, нужен конструктор)
rec := NewReconciler(cl, GinkgoLogr)

// ❌ НЕПРАВИЛЬНО - экспортированные поля, два logger'а
type Reconciler struct {
	Cl     client.Client  // публичное поле
	Log    *slog.Logger   // публичное поле (не используется)
	LogAlt logr.Logger    // публичное поле
}

var _ reconcile.Reconciler = &Reconciler{}  // выделяет память
```

---

## 7. Обработка неиспользуемых параметров

**✅ Используйте:**
- Заменять неиспользуемые параметры на `_` в predicate functions
- Это предотвращает warnings от линтера

**Пример:**
```go
UpdateFunc: func(_ event.UpdateEvent) bool {
	// параметр не используется
	return false
},
DeleteFunc: func(_ event.DeleteEvent) bool {
	// параметр не используется
	return false
},
```

---

## 8. Размещение констант

**✅ Константы, связанные с API:**
- Константы, которые являются частью API (enum значения, алгоритмы, типы) должны быть размещены в API пакете
- Используйте файлы `api/v1alpha3/replicated_volume_consts.go` или `api/v1alpha3/replicated_volume_replica_consts.go` в зависимости от того, к какому ресурсу относятся константы
- Константы должны быть экспортированы (с большой буквы) и иметь документацию
- Если константы используются как список (например, для последовательного перебора), создайте функцию, возвращающую упорядоченный список
- **Важно:** Порядок в списке имеет значение - документируйте это в комментариях

**Пример констант в API:**
```go
// api/v1alpha3/replicated_volume_consts.go
package v1alpha3

// Shared secret hashing algorithms
const (
	// SharedSecretAlgSHA256 is the SHA256 hashing algorithm for shared secrets
	SharedSecretAlgSHA256 = "sha256"
	// SharedSecretAlgSHA1 is the SHA1 hashing algorithm for shared secrets
	SharedSecretAlgSHA1 = "sha1"
)

// SharedSecretAlgorithms returns the ordered list of supported shared secret algorithms.
// The order matters: algorithms are tried sequentially when one fails on any replica.
func SharedSecretAlgorithms() []string {
	return []string{
		SharedSecretAlgSHA256,
		SharedSecretAlgSHA1,
	}
}
```

**⚠️ Константы контроллера (опционально):**
- Если контроллер использует несколько констант, специфичных для логики контроллера (не API), можно вынести их в отдельный файл `consts.go` в директории контроллера
- Это улучшает читаемость и организацию кода
- Константы должны быть связаны с логикой контроллера

**⚠️ Не обязательно:**
- Если константа одна или две, можно оставить их в `reconciler.go`
- Если константы используются только в одном месте, можно оставить их локально

**Обоснование:** Константы API должны быть в API пакете для переиспользования и единообразия. Константы контроллера можно выносить в `consts.go` для улучшения организации кода.

**Пример констант контроллера:**
```go
// consts.go
package mycontroller

const (
	maxNodeID = 7
	minNodeID = 0
)

// reconciler.go
package mycontroller

// Использование констант из consts.go
for i := uint(minNodeID); i <= uint(maxNodeID); i++ {
	// ...
}
```

---

## Структура контроллера

### Принципы упрощения

При создании контроллера следуйте принципу **минимализма**:
- Используйте только необходимые поля в структуре `Reconciler`
- Не добавляйте поля "на будущее" - добавляйте их только когда они реально нужны
- Используйте приватные поля для инкапсуляции
- Используйте конструктор `NewReconciler` для создания экземпляра

### Файлы контроллера:

1. **`controller.go`** - регистрация контроллера
   - `BuildController(mgr manager.Manager) error`
   - Настройка builder с `.For()`, `.Complete()`
   - Инициализация Reconciler через `NewReconciler`

2. **`reconciler.go`** - логика reconcile
   - `Reconciler` struct с минимальными зависимостями (`client.Client`, `logr.Logger`)
   - Поля должны быть приватными (`cl`, `log`) для инкапсуляции
   - Конструктор `NewReconciler` для создания экземпляра
   - Проверка интерфейса: `var _ reconcile.Reconciler = (*Reconciler)(nil)`
   - `Reconcile(ctx, req reconcile.Request)` метод
   - Вспомогательные методы для бизнес-логики (например, `formatValidRange()` для повторяющихся строк)
   - **Важно:** Не добавляйте неиспользуемые поля (`rdr`, `sch`) - они добавляют сложность без пользы
   - **Важно:** Используйте только `logr.Logger`, не используйте два logger'а (`slog.Logger` и `logr.Logger`)
   - **Важно:** Не прячьте работу с клиентом в хелперы. Гораздо удобнее при ревью кода понимать, что вызов работает с клиентом. Если у функции в параметрах нет `ctx context.Context` - это сразу понятно, что она не работает с клиентом
   - **Исключение:** Когда в `Reconcile` принимается решение какой reconcile делать и потом вызываются разные варианты reconcile. В этом случае должно быть видно `return`, что reconcile не пойдет дальше, и функцию лучше назвать `reconcile<вариант>` (например, `reconcileGenerateSecret`, `reconcileHandleError`). Эти функции должны принимать `ctx context.Context` и работать с клиентом напрямую

3. **`consts.go`** (опционально) - константы контроллера
   - Константы, специфичные для логики контроллера (не API константы)
   - Рекомендуется, если констант несколько (2+)
   - Не обязательно, если константа одна или две
   - **Важно:** Константы, связанные с API, должны быть в `api/v1alpha3/replicated_volume_consts.go` или `api/v1alpha3/replicated_volume_replica_consts.go`

4. **`handler.go`** (опционально) - кастомные Watch handlers
   - Используется только если нужны кастомные handlers (например, `handler.EnqueueRequestsFromMapFunc` с кастомной логикой)
   - Выносите логику маппинга событий в отдельные функции
   - Если функции для handlers находятся в `controller.go`, тесты должны быть в `controller_test.go`, а не в `handler_test.go`
   - `handler_test.go` создается только если есть отдельный `handler.go`

5. **`reconciler_test.go`** - unit тесты
   - Тесты с fake Kubernetes client
   - Использовать `reconcile.Request{NamespacedName: types.NamespacedName{Name: "name"}}`
   - Покрытие основных сценариев и edge cases

6. **`controller_test.go`** (опционально) - тесты для функций из `controller.go`
   - Тесты для вспомогательных функций, используемых в `BuildController` (например, функции для predicate)
   - Создается только если есть функции в `controller.go`, которые требуют тестирования
   - **Важно:** Если функция находится в `controller.go`, тесты должны быть в `controller_test.go`, а не в отдельном файле

---

## Лицензионные заголовки

**✅ Обязательно:**
- Все файлы контроллера должны содержать лицензионный заголовок Apache 2.0
- Это включает все `.go` файлы: `controller.go`, `reconciler.go`, `consts.go`, `handler.go`, а также все тестовые файлы `*_test.go`
- Лицензионный заголовок должен быть в начале файла, перед `package` декларацией

**Формат лицензионного заголовка:**
```go
/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mycontroller
```

**Обоснование:** Лицензионные заголовки требуются Apache License 2.0 и обеспечивают консистентность проекта. Все файлы в проекте должны иметь одинаковый формат лицензионного заголовка.

---

## Пример полного контроллера

### controller.go
```go
package mycontroller

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func BuildController(mgr manager.Manager) error {
	rec := NewReconciler(
		mgr.GetClient(),
		mgr.GetLogger().WithName(ControllerName).WithName("Reconciler"),
	)

	return builder.ControllerManagedBy(mgr).
		Named(ControllerName).
		For(&v1alpha3.ReplicatedVolume{}).
		Watches(
			&v1alpha3.ReplicatedVolumeReplica{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&v1alpha3.ReplicatedVolume{},
			),
		).
		Complete(rec)
}
```

**Примечание:** Если нужно только отслеживать основной ресурс без маппинга дочерних, используйте только `.For()`:
```go
	err := builder.ControllerManagedBy(mgr).
		Named("my_controller").
		For(&v1alpha3.SomeResource{}).
		Complete(rec)
```

### reconciler.go
```go
package mycontroller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	cl  client.Client
	log logr.Logger
}

var _ reconcile.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new Reconciler instance.
// This is primarily used for testing, as fields are private.
func NewReconciler(cl client.Client, log logr.Logger) *Reconciler {
	return &Reconciler{
		cl:  cl,
		log: log,
	}
}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.log.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")
	
	obj := &v1alpha3.SomeResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, obj); err != nil {
		log.Error(err, "Getting SomeResource")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	
	// Принятие решения какой reconcile делать
	if obj.Status == nil || obj.Status.SomeField == "" {
		// Видно return - reconcile не пойдет дальше
		return r.reconcileGenerateField(ctx, obj, log)
	}
	
	// Другой вариант reconcile
	return r.reconcileHandleError(ctx, obj, log)
}

// reconcileGenerateField генерирует поле для ресурса
// Название функции показывает, что это вариант reconcile, который не продолжит основной Reconcile
func (r *Reconciler) reconcileGenerateField(
	ctx context.Context,
	obj *v1alpha3.SomeResource,
	log logr.Logger,
) (reconcile.Result, error) {
	// Работа с клиентом напрямую - видно ctx в параметрах
	from := client.MergeFrom(obj)
	changedObj := obj.DeepCopy()
	if changedObj.Status == nil {
		changedObj.Status = &v1alpha3.SomeResourceStatus{}
	}
	changedObj.Status.SomeField = "generated-value"
	
	if err := r.cl.Status().Patch(ctx, changedObj, from); err != nil {
		log.Error(err, "Patching SomeResource status")
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	
	log.Info("Generated field")
	return reconcile.Result{}, nil
}

// reconcileHandleError обрабатывает ошибки
// Название функции показывает, что это вариант reconcile
func (r *Reconciler) reconcileHandleError(
	ctx context.Context,
	obj *v1alpha3.SomeResource,
	log logr.Logger,
) (reconcile.Result, error) {
	// Работа с клиентом напрямую - видно ctx в параметрах
	// ...
	return reconcile.Result{}, nil
}
```

### reconciler_test.go
```go
package mycontroller_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	mycontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/my_controller"
)

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.SomeResource{}).
		Build()
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *mycontroller.Reconciler

	BeforeEach(func() {
		cl = newFakeClient()
		rec = mycontroller.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when resource does not exist", func(ctx context.Context) {
		_, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: "test-resource",
			},
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("handles happy path scenario", func(ctx context.Context) {
		// Arrange
	obj := &v1alpha3.SomeResource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-resource"},
	}
		Expect(cl.Create(ctx, obj)).To(Succeed())
	
		// Act
		_, err := rec.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-resource"},
		})

		// Assert
		Expect(err).NotTo(HaveOccurred())
		// проверки результата
	})
})
```

---

## Чеклист при создании контроллера

- [ ] Использовать стандартный `reconcile.Reconciler` (не TypedReconciler)
- [ ] Использовать `builder.ControllerManagedBy` (не TypedControllerManagedBy)
- [ ] Использовать `.For(&ResourceType{})` для основного ресурса
- [ ] **НЕ использовать `WithEventFilter`** - фильтрация через early return в Reconcile
- [ ] Использовать `EnqueueRequestForOwner` если нужно реагировать на дочерние ресурсы с owner reference
- [ ] Использовать `EnqueueRequestsFromMapFunc` только если нет owner reference, но нужен маппинг
- [ ] Использовать structured logger с `.WithName()` и `.WithValues()` в Reconcile
- [ ] **Упрощенная структура Reconciler:** только `cl`, `log` (приватные поля, только `logr.Logger`)
- [ ] **Конструктор `NewReconciler`:** для создания экземпляра в `controller.go` и тестах
- [ ] **Проверка интерфейса:** `var _ reconcile.Reconciler = (*Reconciler)(nil)` (nil pointer)
- [ ] **Использование `slices.DeleteFunc`:** для фильтрации слайсов вместо ручной фильтрации
- [ ] **Использование `map[K]struct{}`:** для set-подобных структур вместо `map[K]bool`
- [ ] **Инициализация status:** внутри `patchFn`, а не в начале `Reconcile`
- [ ] **Использование PatchStatusWithConflictRetry:** для всех обновлений status
- [ ] **Идемпотентность:** проверка состояния в начале Reconcile и внутри patchFn
- [ ] Добавить unit тесты с **Ginkgo/Gomega**
- [ ] Использовать fake client с `.WithStatusSubresource()`
- [ ] Использовать `reconcile.Request` в тестах
- [ ] Зарегистрировать контроллер в `registry.go`
- [ ] **Лицензионные заголовки:** Все файлы (включая тестовые) содержат лицензионный заголовок Apache 2.0
- [ ] **Константы API:** Константы, связанные с API, размещены в `api/v1alpha3/replicated_volume_consts.go` или `api/v1alpha3/replicated_volume_replica_consts.go`
- [ ] **Тесты рядом с кодом:** Тесты для функций из `controller.go` находятся в `controller_test.go`, а не в отдельных файлах

---

## Регистрация контроллера

После создания контроллера, зарегистрируйте его в `images/controller/internal/controllers/registry.go`:

```go
import (
	mycontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/my_controller"
)

func init() {
	registry = append(registry, mycontroller.BuildController)
	// ...
}
```

---

## Дополнительные рекомендации

### Обработка ошибок
- Использовать типизированные ошибки из `internal/errors`
- Возвращать `reconcile.Result{}` с ошибкой для retry
- Логировать ошибки через structured logger
- **Важно:** Сначала создайте ошибку, затем залогируйте её и верните. Это позволяет передать ошибку в лог как контекст.

**Пример:**
```go
// ✅ ПРАВИЛЬНО - сначала создать ошибку, потом залогировать
err := e.ErrInvalidClusterf(
    "too many replicas for volume %s: %d (maximum is %d)",
    rvr.Spec.ReplicatedVolumeName,
    totalReplicas,
    MaxNodeID+1,
)
log.Error(err, "too many replicas for volume", "volume", rvr.Spec.ReplicatedVolumeName, "replicas", totalReplicas, "max", MaxNodeID+1)
return reconcile.Result{}, err

// ❌ НЕПРАВИЛЬНО - логирование перед созданием ошибки
log.Error(nil, "too many replicas for volume", "volume", rvr.Spec.ReplicatedVolumeName, "replicas", totalReplicas, "max", MaxNodeID+1)
return reconcile.Result{}, e.ErrInvalidClusterf(...)
```

### Конфликты при обновлении
- **НЕ используйте** `api.PatchStatusWithConflictRetry` или `api.PatchWithConflictRetry`
- Используйте простой `client.Status().Patch()` или `client.Patch()`
- При конфликте (409) возвращайте ошибку - следующий reconciliation решит проблему
- Это глобальное правило для всех контроллеров: конфликты обрабатываются через повторный reconciliation, а не через retry внутри одного вызова
- **Важно:** Инициализация status должна происходить **перед** Patch, используя `DeepCopy()` для создания изменяемой копии

**Пример:**
```go
// ❌ НЕПРАВИЛЬНО - использование PatchStatusWithConflictRetry
if err := api.PatchStatusWithConflictRetry(ctx, r.cl, obj, func(currentObj *v1alpha3.SomeResource) error {
    if currentObj.Status == nil {
        currentObj.Status = &v1alpha3.SomeResourceStatus{}
    }
    currentObj.Status.SomeField = value
    return nil
}); err != nil {
    return reconcile.Result{}, fmt.Errorf("updating status: %w", err)
}

// ✅ ПРАВИЛЬНО - простой Patch с обработкой конфликтов через reconciliation
// Важно: MergeFrom должен получать оригинальный объект, а не DeepCopy
from := client.MergeFrom(obj)
changedObj := obj.DeepCopy()
if changedObj.Status == nil {
    changedObj.Status = &v1alpha3.SomeResourceStatus{}
}
changedObj.Status.SomeField = value

if err := r.cl.Status().Patch(ctx, changedObj, from); err != nil {
    log.Error(err, "Patching resource status")
    return reconcile.Result{}, client.IgnoreNotFound(err)
}
log.Info("Updated status", "field", value)
```

**Обоснование:**
- Упрощает код: нет сложной логики retry
- Снижает нагрузку на API: нет множественных попыток в одном reconciliation
- Конфликты редки в типичных сценариях, следующий reconciliation быстро решит проблему
- Соответствует паттерну "fail fast, retry through reconciliation"
- **Важно:** На retry должен уходить весь reconciliation, а не только Patch. Ресурс может быть удален пока мы его патчим, и это должно быть обработано в следующем reconciliation

**Обработка Race Conditions:**
- При одновременных reconcile нескольких ресурсов может возникнуть race condition
- Если два reconcile пытаются установить одинаковое значение (например, deviceMinor), один получит 409 Conflict
- Это нормальное поведение: следующий reconciliation пересчитает состояние и выберет другое значение
- **Не нужно** использовать мьютексы или другие механизмы синхронизации - Kubernetes API уже обеспечивает optimistic locking через ResourceVersion
- **Тестирование:** Симулируйте 409 Conflict через interceptors в тестах для проверки обработки race conditions

**Теоретические проблемы с Optimistic Locking:**
- **High Contention Scenarios:** При высокой конкуренции (много одновременных обновлений одного ресурса) увеличивается вероятность 409 Conflict
- **В нашем случае это не проблема:** Конфликты редки, так как каждый контроллер обычно обновляет свой ресурс
- **Обработка через reconciliation:** При 409 Conflict следующий reconciliation пересчитает состояние и выберет другое значение
- **Optimistic locking работает надежно:** Kubernetes API гарантирует атомарность проверки ResourceVersion на стороне API сервера
- **Edge cases:** В очень редких случаях при высокой нагрузке может потребоваться несколько retry, но это нормально и обрабатывается автоматически через reconciliation retry mechanism

**Пример обработки race condition:**
```go
// Reconcile 1 и Reconcile 2 одновременно делают List
rvList := &v1alpha3.ReplicatedVolumeList{}
if err := r.cl.List(ctx, rvList); err != nil {
    return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
}

// Оба видят одинаковый список, оба выбирают deviceMinor = 0
usedDeviceMinors := collectUsedDeviceMinors(rvList)
availableDeviceMinor := findAvailable(usedDeviceMinors) // = 0

// Reconcile 1 успешно делает Patch → устанавливает deviceMinor = 0
// Reconcile 2 пытается сделать Patch → получает 409 Conflict (ResourceVersion изменился)
if err := r.cl.Status().Patch(ctx, changedRV, from); err != nil {
    // 409 Conflict вернется как ошибка
    log.Error(err, "Patching ReplicatedVolume status")
    return reconcile.Result{}, err // Следующий reconciliation решит проблему
}

// Reconcile 2 retry → делает Get → видит, что deviceMinor = 0 уже занят
// → выбирает deviceMinor = 1 → успешно устанавливает
```

### Идемпотентность
- Reconcile должен быть идемпотентным
- Проверять текущее состояние перед изменениями (ранний выход, если уже установлено)
- Не делать лишних обновлений, если состояние уже корректное
- При конфликте (409) следующий reconciliation проверит состояние заново и при необходимости обновит

**Пример:**
```go
// Проверка в начале Reconcile
if obj.Status != nil && obj.Status.SomeField != nil {
    log.V(1).Info("field already set", "field", *obj.Status.SomeField)
    return reconcile.Result{}, nil
}

// ✅ ПРАВИЛЬНО - проверка перед Patch (idempotent check)
from := client.MergeFrom(obj)
changedObj := obj.DeepCopy()
if changedObj.Status == nil {
    changedObj.Status = &v1alpha3.SomeResourceStatus{}
}
// Проверка еще раз на случай race condition (если другой reconcile уже установил)
if changedObj.Status.SomeField != nil {
    log.V(1).Info("field already set by another worker")
    return reconcile.Result{}, nil // Уже установлено, ничего не делаем
}
changedObj.Status.SomeField = &value

if err := r.cl.Status().Patch(ctx, changedObj, from); err != nil {
    log.Error(err, "Patching resource status")
    return reconcile.Result{}, client.IgnoreNotFound(err)
}
```

### Оптимизации кода

**⚠️ Важно:** После написания кода проверьте его на возможные оптимизации, которые остаются в рамках code style. Оптимизации опциональны, но лучше, если ревьюер будет доволен.

**Рекомендации по оптимизации:**

1. **Ранний выход из циклов:**
   - Если нашли нужный элемент или условие выполнено, выходите из цикла сразу
   - Не продолжайте итерацию, если результат уже получен

2. **Проверка до дорогих операций:**
   - Если можно проверить условие до `List` или других дорогих операций, делайте это
   - Например, если текущий ресурс уже имеет нужное значение, не нужно делать `List` всех ресурсов

3. **Избегание лишних итераций:**
   - Если собираете данные в цикле, но нашли нужное значение, выходите раньше
   - Не собирайте все данные, если они не нужны

**Примеры оптимизаций:**

```go
// ❌ НЕОПТИМАЛЬНО - делаем List, затем проверяем
rvList := &v1alpha3.ReplicatedVolumeList{}
if err := r.cl.List(ctx, rvList); err != nil {
    return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
}

usedDeviceMinors := make(map[uint]struct{})
currentRVHasDeviceMinor := false
for _, item := range rvList.Items {
    if item.Status != nil && item.Status.DRBD != nil && item.Status.DRBD.Config != nil {
        deviceMinor := item.Status.DRBD.Config.DeviceMinor
        if deviceMinor >= MinDeviceMinor && deviceMinor <= MaxDeviceMinor {
            usedDeviceMinors[deviceMinor] = struct{}{}
            if item.Name == rv.Name {
                currentRVHasDeviceMinor = true
            }
        }
    }
}

if currentRVHasDeviceMinor {
    return reconcile.Result{}, nil
}

// ✅ ОПТИМАЛЬНО - проверяем до List
if rv.Status != nil && rv.Status.DRBD != nil && rv.Status.DRBD.Config != nil {
    deviceMinor := rv.Status.DRBD.Config.DeviceMinor
    if deviceMinor >= MinDeviceMinor && deviceMinor <= MaxDeviceMinor {
        log.V(1).Info("deviceMinor already assigned", "deviceMinor", deviceMinor)
        return reconcile.Result{}, nil // Выходим до List
    }
}

// Теперь делаем List только если нужно
rvList := &v1alpha3.ReplicatedVolumeList{}
if err := r.cl.List(ctx, rvList); err != nil {
    return reconcile.Result{}, fmt.Errorf("listing RVs: %w", err)
}
```

```go
// ❌ НЕОПТИМАЛЬНО - собираем все данные, даже если нашли нужное
for _, item := range items {
    if item.Name == targetName {
        found = true
    }
    // Продолжаем собирать остальные данные, хотя уже нашли нужное
    allData[item.Name] = item
}

if found {
    return reconcile.Result{}, nil
}

// ✅ ОПТИМАЛЬНО - выходим сразу после нахождения
for _, item := range items {
    if item.Name == targetName {
        log.V(1).Info("found target", "name", targetName)
        return reconcile.Result{}, nil // Выходим сразу
    }
}
```

**Когда оптимизировать:**
- Если оптимизация улучшает читаемость и не усложняет код - делайте
- Если оптимизация делает код сложнее - обсудите с командой
- Оптимизации опциональны, но ревьюеры могут попросить их добавить

### Параллельная обработка (обсуждаемо)

**⚠️ Обсуждаемо:** Поддержка параллельной обработки через goroutines.

**Текущий подход:**
- Controller-runtime по умолчанию обрабатывает запросы параллельно (несколько воркеров)
- Используйте `PatchStatusWithConflictRetry` / `PatchWithConflictRetry` для безопасных обновлений
- Эти функции используют optimistic locking и автоматически обрабатывают конфликты

**Возможные улучшения (требуют обсуждения):**
- Явное управление параллелизмом через goroutines для независимых операций
- Использование worker pools для обработки множества ресурсов
- Параллельная обработка нескольких ресурсов одного типа

**Рекомендация:** Начинайте с стандартного подхода controller-runtime. Рассматривайте явную параллелизацию только если:
- Есть доказанная необходимость (производительность)
- Операции действительно независимы
- Обсуждено с командой

**Пример безопасной параллельной обработки (обсуждаемо):**
```go
// Внутри Reconcile, если нужно обработать несколько ресурсов параллельно
// ⚠️ Используйте только после обсуждения с командой
var wg sync.WaitGroup
errCh := make(chan error, len(resources))

for _, resource := range resources {
    wg.Add(1)
    go func(r Resource) {
        defer wg.Done()
        if err := processResource(ctx, r); err != nil {
            errCh <- err
        }
    }(resource)
}

wg.Wait()
close(errCh)

// Проверить ошибки
for err := range errCh {
    if err != nil {
        return reconcile.Result{}, err
    }
}
```

**Важно:** При использовании goroutines убедитесь, что:
- Операции действительно независимы
- Нет race conditions при доступе к общим ресурсам
- Используется правильная обработка ошибок и контекста
- Это обсуждено с командой перед реализацией

### Тестирование

**✅ Используйте по умолчанию:**
- **Ginkgo/Gomega** для структурированных тестов
- Fake client для изоляции тестов
- `.WithStatusSubresource()` для корректного тестирования status updates
- **Вложенные `When` блоки** для группировки связанных тестов
- **`By()` вместо комментариев** для структурирования шагов теста (отображается в выводе тестов)
- **Второй аргумент в `Expect()`** для объяснений (где это не очевидно)
- **`JustBeforeEach` и `clientBuilder` паттерн** для гибкой настройки fake client с interceptors
- **`SpecContext`** вместо `context.Context` для таймаутов от Ginkgo
- **`DescribeTableSubtree`** для тестирования edge cases с похожей логикой
- **Interceptors** для тестирования ошибок API (Get, List, Patch)
- **Отдельный `suite_test.go`** с хелперами (`RequestFor`, `Requeue`, `InterceptGet`)

**Структура тестов:**

Используйте иерархическую структуру с `When` блоками для группировки тестов по сценариям:

```go
package mycontroller_test

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha3 "github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	mycontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/my_controller"
)

func newFakeClient() client.Client {
	scheme := runtime.NewScheme()
	_ = v1alpha3.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.SomeResource{}).
		Build()
}

// Helper functions for creating test resources
func createResource(name string) *v1alpha3.SomeResource {
	return &v1alpha3.SomeResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

var _ = Describe("Reconciler", func() {
	var cl client.Client
	var rec *mycontroller.Reconciler

	BeforeEach(func() {
		cl = newFakeClient()
		rec = mycontroller.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when resource does not exist", func(ctx SpecContext) {
		By("Reconciling non-existent resource")
		result, err := rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})
		Expect(err).NotTo(HaveOccurred(), "should ignore NotFound errors")
		Expect(result).ToNot(Requeue(), "should not requeue when resource does not exist")
	})

	When("resource created", func() {
		When("resource without required field", func() {
			It("assigns default value to first resource", func(ctx SpecContext) {
				By("Creating first resource without required field")
				obj := createResource("resource-1")
				Expect(cl.Create(ctx, obj)).To(Succeed())

				By("Reconciling first resource")
				result, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "resource-1"},
				})
				Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
				Expect(result).ToNot(Requeue(), "should not requeue after successful assignment")

				By("Verifying default value was assigned")
				updatedObj := &v1alpha3.SomeResource{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "resource-1"}, updatedObj)).To(Succeed())
				Expect(updatedObj.Status.SomeField).NotTo(BeNil(), "default value should be assigned")
			})

			It("assigns values sequentially", func(ctx SpecContext) {
				By("Creating resources: one with value 0, one without value")
				obj1 := createResourceWithValue("resource-1", 0)
				obj2 := createResource("resource-2")
				Expect(cl.Create(ctx, obj1)).To(Succeed())
				Expect(cl.Create(ctx, obj2)).To(Succeed())

				By("Reconciling second resource")
				result, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "resource-2"},
				})
				Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
				Expect(result).ToNot(Requeue(), "should not requeue after successful assignment")

				By("Verifying sequential assignment: next available value after 0")
				updatedObj := &v1alpha3.SomeResource{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "resource-2"}, updatedObj)).To(Succeed())
				Expect(updatedObj.Status.SomeField).To(Equal(1), "should assign value 1 as next sequential value")
			})

			When("limit exceeded", func() {
				It("returns error when limit exceeded", func(ctx SpecContext) {
					By("Creating N resources at limit")
					for i := 0; i < maxResources; i++ {
						obj := createResourceWithValue(fmt.Sprintf("resource-%d", i+1), uint(i))
						Expect(cl.Create(ctx, obj)).To(Succeed())
					}

					By("Creating (N+1)th resource that should exceed limit")
					objNPlus1 := createResource("resource-n+1")
					Expect(cl.Create(ctx, objNPlus1)).To(Succeed())

					By("Reconciling (N+1)th resource should fail")
					_, err := rec.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: "resource-n+1"},
					})
					Expect(err).To(HaveOccurred(), "should return error when limit exceeded")
				})
			})
		})

		When("resource with value already assigned", func() {
			It("does not reassign if already assigned", func(ctx SpecContext) {
				By("Creating resource with value already assigned")
				obj := createResourceWithValue("resource-1", 3)
				Expect(cl.Create(ctx, obj)).To(Succeed())

				By("Reconciling resource with existing value")
				result, err := rec.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: "resource-1"},
				})
				Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed")
				Expect(result).ToNot(Requeue(), "should not requeue when value already assigned")

				By("Verifying value remains unchanged (idempotent)")
				updatedObj := &v1alpha3.SomeResource{}
				Expect(cl.Get(ctx, client.ObjectKey{Name: "resource-1"}, updatedObj)).To(Succeed())
				Expect(updatedObj.Status.SomeField).To(Equal(3), "value should remain 3, not be reassigned")
			})
		})
	})
})

})
```

**Современные паттерны тестирования:**

1. **`JustBeforeEach` и `clientBuilder` паттерн:**
   - Используйте `BeforeEach` для настройки `clientBuilder` и схемы
   - Используйте `JustBeforeEach` для создания клиента ПОСЛЕ всех настроек (включая interceptors)
   - Это позволяет добавлять interceptors в `BeforeEach` вложенных блоков перед созданием клиента

```go
BeforeEach(func() {
	scheme = runtime.NewScheme()
	Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
	clientBuilder = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha3.SomeResource{})
})

JustBeforeEach(func() {
	cl = clientBuilder.Build()
	rec = mycontroller.NewReconciler(cl, GinkgoLogr)
})

When("Get fails", func() {
	BeforeEach(func() {
		// Добавляем interceptor ПЕРЕД созданием клиента
		clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(...))
	})
	
	It("should fail", func(ctx SpecContext) {
		// Клиент создается в JustBeforeEach с interceptor'ом
	})
})
```

2. **`SpecContext` вместо `context.Context`:**
   - Используйте `SpecContext` в тестах - это `context.Context` с таймаутом от Ginkgo
   - Если тест зависнет, Ginkgo автоматически прервет его

3. **`DescribeTableSubtree` для edge cases:**
   - Используйте для тестирования похожих сценариев с разными входными данными
   - Уменьшает дублирование кода

```go
DescribeTableSubtree("when resource has",
	Entry("nil Status", func() { obj.Status = nil }),
	Entry("nil Status.Field", func() { obj.Status = &Status{Field: nil} }),
	func(setup func()) {
		// ✅ ПРАВИЛЬНО - передаем функцию напрямую
		BeforeEach(setup)
		
		It("should handle nil fields", func(ctx SpecContext) {
			// Один тест для всех Entry
		})
	})
```

**Важно:** В `DescribeTableSubtree` передавайте функцию `setup` напрямую в `BeforeEach`, а не оборачивайте её в анонимную функцию:
- ✅ **ПРАВИЛЬНО:** `BeforeEach(setup)`
- ❌ **НЕПРАВИЛЬНО:** `BeforeEach(func() { setup() })`

Это делает код более лаконичным и соответствует best practices Ginkgo.

4. **Interceptors для тестирования ошибок API:**
   - Используйте interceptors для симуляции ошибок Get, List, Patch
   - Это позволяет тестировать обработку ошибок без реального Kubernetes
   - **Важно:** Patch ошибки МОЖНО тестировать через `SubResourcePatch` interceptor
   - ❌ **НЕПРАВИЛЬНО:** Комментарии типа "Testing Patch errors with fake client is complex" или "Fake client's Status().Patch() doesn't easily allow injecting errors" - это неверно
   - ✅ **ПРАВИЛЬНО:** Используйте `SubResourcePatch` interceptor для симуляции Patch ошибок, включая 409 Conflict

**Пример теста для Get ошибок:**
```go
When("Get fails", func() {
	BeforeEach(func() {
		clientBuilder = clientBuilder.WithInterceptorFuncs(
			InterceptGet(func(_ *v1alpha3.SomeResource) error {
				return errors.New("internal server error")
			})
		)
	})
	
	It("should return error", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(obj))).Error().To(HaveOccurred())
	})
})
```

**Пример теста для Patch ошибок (обычные ошибки):**
```go
When("Patch fails with non-NotFound error", func() {
	var rv *v1alpha3.ReplicatedVolume
	patchError := errors.New("failed to patch status")

	BeforeEach(func() {
		rv = createRV("volume-patch-1")
		clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if _, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
					if subResourceName == "status" {
						return patchError
					}
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})
	})

	JustBeforeEach(func(ctx SpecContext) {
		Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
	})

	It("should fail if patching ReplicatedVolume status failed with non-NotFound error", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(patchError), "should return error when Patch fails")
	})
})
```

**Пример теста для Patch ошибок (409 Conflict - race condition):**
```go
When("Patch fails with 409 Conflict (race condition)", func() {
	var rv *v1alpha3.ReplicatedVolume
	var conflictError error
	var patchAttempts int

	BeforeEach(func() {
		rv = createRV("volume-conflict-1")
		patchAttempts = 0
		// Simulate 409 Conflict error (race condition scenario)
		conflictError = kerrors.NewConflict(
			schema.GroupResource{Group: "storage.deckhouse.io", Resource: "replicatedvolumes"},
			rv.Name,
			errors.New("resourceVersion conflict: the object has been modified"),
		)
		clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, cl client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if rvObj, ok := obj.(*v1alpha3.ReplicatedVolume); ok {
					if subResourceName == "status" && rvObj.Name == rv.Name {
						patchAttempts++
						// Simulate conflict on first patch attempt only
						if patchAttempts == 1 {
							return conflictError
						}
						// Allow subsequent attempts to succeed (simulating retry after conflict)
					}
				}
				return cl.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		})
	})

	JustBeforeEach(func(ctx SpecContext) {
		Expect(cl.Create(ctx, rv)).To(Succeed(), "should create ReplicatedVolume")
	})

	It("should return error on 409 Conflict and succeed on retry", func(ctx SpecContext) {
		By("First reconcile: should fail with 409 Conflict")
		Expect(rec.Reconcile(ctx, RequestFor(rv))).Error().To(MatchError(conflictError), "should return conflict error on first attempt")

		By("Second reconcile (retry): should succeed after conflict resolved")
		Expect(rec.Reconcile(ctx, RequestFor(rv))).ToNot(Requeue(), "retry reconciliation should succeed")

		By("Verifying field was assigned after retry")
		updatedRV := &v1alpha3.ReplicatedVolume{}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(rv), updatedRV)).To(Succeed(), "should get updated ReplicatedVolume")
		Expect(updatedRV).To(HaveField("Status.SomeField", Not(BeEmpty())), "field should be assigned")
	})
})
```

**Импорты для теста 409 Conflict:**
```go
import (
	"errors"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)
```

5. **Отдельный `suite_test.go` с хелперами:**
   - Создавайте `suite_test.go` с общими хелперами и матчерами
   - Примеры: `RequestFor()`, `Requeue()`, `InterceptGet()`

```go
// suite_test.go
func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func InterceptGet[T client.Object](intercept func(T) error) interceptor.Funcs {
	// Реализация interceptor'а
}
```

6. **Использование хелперов в тестах:**
   - Используйте `RequestFor()` вместо ручного создания `reconcile.Request`
   - Используйте `Requeue()` для проверки requeue

```go
It("should reconcile", func(ctx SpecContext) {
	Expect(rec.Reconcile(ctx, RequestFor(obj))).ToNot(Requeue())
	// Вместо:
	// Expect(rec.Reconcile(ctx, reconcile.Request{
	//     NamespacedName: client.ObjectKeyFromObject(obj),
	// })).To(Equal(reconcile.Result{}))
})
```

**Пример полной структуры с современными паттернами:**

```go
// suite_test.go
package mycontroller_test

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestMyController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MyController Suite")
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

func InterceptGet[T client.Object](intercept func(T) error) interceptor.Funcs {
	// Реализация interceptor'а
}

// reconciler_test.go
var _ = Describe("Reconciler", func() {
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
		cl            client.WithWatch
		rec           *mycontroller.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed())
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.SomeResource{})
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = mycontroller.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when resource does not exist", func(ctx SpecContext) {
		Expect(rec.Reconcile(ctx, RequestFor(obj))).ToNot(Requeue())
	})
})
```

**Ключевые принципы структуры тестов:**

1. **Иерархическая организация:**
   - `Describe("Reconciler")` - основной блок
   - `When("condition")` - группировка тестов по сценариям
   - `It("test description")` - конкретный тест

2. **Структура с базовыми ресурсами в `BeforeEach` и созданием в `JustBeforeEach`:**
   - Объявляйте базовые ресурсы как Go объекты в `BeforeEach` родительского `When` блока
   - Создавайте ресурсы в fake client в `JustBeforeEach` (только если они не `nil`)
   - Списки ресурсов (`rvrList`) инициализируйте как `nil` в родительском `BeforeEach` и заполняйте в дочерних `When` блоках
   - Это позволяет модифицировать ресурсы в дочерних тестах перед созданием
   - Пример:
     ```go
     When("RVR created", func() {
         var (
             rvr      *v1alpha3.ReplicatedVolumeReplica
             rvrList  []v1alpha3.ReplicatedVolumeReplica
             otherRVR *v1alpha3.ReplicatedVolumeReplica
         )

         BeforeEach(func() {
             // Base RVR - создается как Go объект, может быть модифицирован в дочерних тестах
             rvr = &v1alpha3.ReplicatedVolumeReplica{
                 ObjectMeta: metav1.ObjectMeta{Name: "rvr-1"},
                 Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
                     ReplicatedVolumeName: "volume-1",
                     NodeName:             "node-1",
                 },
             }
             // Initialize empty list - будет заполнен в дочерних тестах
             rvrList = nil
         })

         JustBeforeEach(func(ctx SpecContext) {
             // Создаем только базовый RVR (если не nil)
             if rvr != nil {
                 Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create base RVR")
             }
             // rvrList и otherRVR создаются только в дочерних тестах где нужны
         })

         When("multiple RVRs exist", func() {
             BeforeEach(func() {
                 // Заполняем список в дочернем тесте
                 rvrList = []v1alpha3.ReplicatedVolumeReplica{...}
             })

             JustBeforeEach(func(ctx SpecContext) {
                 // Создаем список в fake client
                 for i := range rvrList {
                     Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
                 }
             })
         })
     })
     ```

3. **Использование `By()` для структурирования тестов:**
   - Используйте `By("описание шага")` вместо комментариев для структурирования тестов
   - `By()` отображается в выводе тестов при запуске с `-ginkgo.v` флагом
   - Это делает тесты самодокументируемыми и улучшает читаемость вывода
   - **Важно:** Используйте `By()` только для этапов теста (Reconciling, Verifying, Deleting), НЕ используйте в `JustBeforeEach` для создания ресурсов
   - **Важно:** НЕ используйте `By()` в простых одиночных тестах - это визуальный шум. Используйте `By()` только когда в тесте несколько шагов.
   - Примеры:
     ```go
     // ✅ ПРАВИЛЬНО - простой тест без By()
     It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
         Expect(rec.Reconcile(ctx, reconcile.Request{
             NamespacedName: types.NamespacedName{Name: "non-existent"},
         })).NotTo(Requeue(), "should ignore NotFound errors")
     })
     
     // ✅ ПРАВИЛЬНО - многошаговый тест с By()
     It("assigns nodeID successfully", func(ctx SpecContext) {
         By("Reconciling ReplicatedVolumeReplica with nil status fields")
         Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should not requeue after successful assignment")
         
         By("Verifying nodeID was assigned")
         updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
         Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
         Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", MinNodeID))), "first replica should get nodeID MinNodeID")
     })
     
     // ❌ НЕПРАВИЛЬНО - By() в простом одиночном тесте
     It("should fail if getting ReplicatedVolumeReplica failed", func(ctx SpecContext) {
         By("Reconciling with Get interceptor that returns error")
         Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(internalServerError))
     })
     ```

4. **Второй аргумент в `Expect()` для объяснений:**
   - Добавляйте второй аргумент в `Expect()` где это не очевидно
   - Это улучшает читаемость и помогает понять, почему ожидается именно это значение
   - Используйте для:
     - Проверок конкретных значений (почему ожидается именно это значение)
     - Проверок `Requeue()` (что означает успешное завершение)
     - Проверок ошибок (что это правильная обработка ошибки)
     - Проверок инициализации статуса (что поля должны быть инициализированы)
   - Примеры:
     ```go
     Expect(err).NotTo(HaveOccurred(), "should ignore NotFound errors")
     Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should not requeue after successful assignment")
     Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
     Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", MinNodeID))), "first replica should get nodeID MinNodeID")
     Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(expectedError), "should return error when Get fails")
     ```

5. **Использование `HaveField` matchers:**
   - Используйте `HaveField` для проверки вложенных полей вместо множественных `Expect`
   - Это делает тесты более читаемыми и компактными
   - Примеры:
     ```go
     // ✅ ПРАВИЛЬНО - один Expect с HaveField
     Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", MinNodeID))), "first replica should get nodeID MinNodeID")
     
     // ❌ НЕПРАВИЛЬНО - множественные Expect
     Expect(updatedRVR.Status).NotTo(BeNil())
     Expect(updatedRVR.Status.DRBD).NotTo(BeNil())
     Expect(updatedRVR.Status.DRBD.Config).NotTo(BeNil())
     Expect(updatedRVR.Status.DRBD.Config.NodeId).NotTo(BeNil())
     Expect(*updatedRVR.Status.DRBD.Config.NodeId).To(Equal(MinNodeID))
     ```

6. **Использование `Error().To(MatchError(...))` для проверки ошибок:**
   - Используйте `Expect(...).Error().To(MatchError(...))` для проверки ошибок
   - Это более читаемо и явно показывает, что ожидается ошибка
   - Примеры:
     ```go
     // ✅ ПРАВИЛЬНО
     Expect(rec.Reconcile(ctx, RequestFor(rvr))).Error().To(MatchError(expectedError), "should return error when Get fails")
     
     // ❌ НЕПРАВИЛЬНО
     _, err := rec.Reconcile(ctx, RequestFor(rvr))
     Expect(err).To(HaveOccurred())
     Expect(errors.Is(err, expectedError)).To(BeTrue())
     ```

7. **Использование `types.NamespacedName` в `RequestFor`:**
   - Используйте `types.NamespacedName` в `RequestFor` helper вместо `client.ObjectKey`
   - Это соответствует стилю референсной ветки
   - Пример:
     ```go
     // suite_test.go
     func RequestFor(object client.Object) reconcile.Request {
         return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
     }
     
     // В тестах
     Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue())
     ```

8. **Использование `client.ObjectKeyFromObject` для `Get`/`Delete`:**
   - Используйте `client.ObjectKeyFromObject(obj)` вместо ручного создания `client.ObjectKey`
   - Это более безопасно и читаемо
   - **Важно:** Если объект уже существует и будет использоваться после `Get`, не создавайте новый объект - используйте существующий
   - Примеры:
     ```go
     // ✅ ПРАВИЛЬНО - использование существующего объекта
     Expect(cl.Get(ctx, client.ObjectKeyFromObject(otherRVR), otherRVR)).To(Succeed(), "should get updated RVR")
     Expect(otherRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", MinNodeID))))
     
     // ✅ ПРАВИЛЬНО - создание нового объекта, если нужен для проверки
     updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
     Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
     Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", MinNodeID))))
     
     // ❌ НЕПРАВИЛЬНО - создание нового объекта, когда можно использовать существующий
     updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
     Expect(cl.Get(ctx, client.ObjectKeyFromObject(otherRVR), updatedRVR)).To(Succeed())
     Expect(updatedRVR).To(HaveField(...)) // otherRVR больше не нужен
     
     // ❌ НЕПРАВИЛЬНО - ручное создание ObjectKey
     Expect(cl.Get(ctx, client.ObjectKey{Name: "rvr-1"}, updatedRVR)).To(Succeed())
     ```

9. **Упрощение `Get` error handling:**
   - Используйте `client.IgnoreNotFound(err)` напрямую, не проверяйте `client.IgnoreNotFound(err) == nil`
   - Используйте `client.IsNotFound(err)` если нужно явно проверить NotFound
   - Примеры:
     ```go
     // ✅ ПРАВИЛЬНО - в reconciler.go
     if err := r.cl.Get(ctx, req.NamespacedName, &rvr); err != nil {
         log.Error(err, "Getting ReplicatedVolumeReplica")
         return reconcile.Result{}, client.IgnoreNotFound(err)
     }
     
     // ✅ ПРАВИЛЬНО - в тестах для явной проверки
     if client.IsNotFound(err) {
         // handle NotFound
     }
     ```

10. **Группировка по сценариям:**
    - Используйте `When` блоки для логической группировки
    - Примеры: `When("resource without required field")`, `When("limit exceeded")`, `When("resource with value already assigned")`

11. **Вынесение повторяющихся `JustBeforeEach` в общий блок:**
    - Если несколько `When` блоков имеют одинаковый `JustBeforeEach` (например, создание списка ресурсов), вынесите его в родительский `When` блок
    - Это уменьшает дублирование кода и улучшает читаемость
    - Блоки с дополнительной логикой в `JustBeforeEach` оставьте как есть
    - Пример:
     ```go
     When("multiple resources exist", func() {
         var resourceList []v1alpha3.SomeResource
         
         JustBeforeEach(func(ctx SpecContext) {
             if resourceList != nil {
                 for i := range resourceList {
                     Expect(cl.Create(ctx, &resourceList[i])).To(Succeed(), "should create resource successfully")
                 }
             }
         })
         
         When("assigning sequentially", func() {
             BeforeEach(func() {
                 resourceList = make([]v1alpha3.SomeResource, 5)
                 // ... инициализация
             })
             // JustBeforeEach наследуется от родителя
         })
         
         When("filling gaps", func() {
             BeforeEach(func() {
                 resourceList = []v1alpha3.SomeResource{...}
             })
             // JustBeforeEach наследуется от родителя
         })
         
         When("with additional logic", func() {
             BeforeEach(func() {
                 resourceList = []v1alpha3.SomeResource{...}
             })
             
             JustBeforeEach(func(ctx SpecContext) {
                 // Дополнительная логика, переопределяет родительский
                 By("Ensuring parent resource has value")
                 Expect(rec.Reconcile(ctx, RequestFor(parentResource))).ToNot(Requeue())
             })
         })
     })
     ```

12. **Arrange-Act-Assert паттерн:**
    - Используйте `By()` для явного разделения шагов (только в многошаговых тестах):
      - **Arrange** - подготовка данных (обычно в `BeforeEach`/`JustBeforeEach`, без `By()`)
      - **Act** - выполнение действия (`By("Reconciling resource")`)
      - **Assert** - проверка результата (`By("Verifying result")`)

**Покрытие:**
- Основные сценарии (happy path)
- Edge cases (ошибки, граничные условия, nil поля)
- Идемпотентность
- Обработка ошибок API (Get, List, Patch)
- Освобождение и повторное использование ресурсов (если применимо)
- Тестирование ошибок через interceptors

**Реальный пример структуры:**

Пример из `rvr-status-config-node-id-controller` с современными паттернами:

```go
// suite_test.go
package rvrstatusconfignodeid_test

import (
	"context"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gomegatypes "github.com/onsi/gomega/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRvrStatusConfigNodeId(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RvrStatusConfigNodeId Suite")
}

func RequestFor(object client.Object) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKeyFromObject(object)}
}

func Requeue() gomegatypes.GomegaMatcher {
	return Not(Equal(reconcile.Result{}))
}

// InterceptGet creates an interceptor that modifies objects in both Get and List operations.
func InterceptGet[T client.Object](
	intercept func(T) error,
) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			targetObj, ok := obj.(T)
			if !ok {
				return cl.Get(ctx, key, obj, opts...)
			}
			if err := cl.Get(ctx, key, obj, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			if err := intercept(targetObj); err != nil {
				return err
			}
			return nil
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			v := reflect.ValueOf(list).Elem()
			itemsField := v.FieldByName("Items")
			if !itemsField.IsValid() || itemsField.Kind() != reflect.Slice {
				return cl.List(ctx, list, opts...)
			}
			if err := cl.List(ctx, list, opts...); err != nil {
				var zero T
				if err := intercept(zero); err != nil {
					return err
				}
				return err
			}
			for i := 0; i < itemsField.Len(); i++ {
				item := itemsField.Index(i).Addr().Interface().(client.Object)
				if targetObj, ok := item.(T); ok {
					if err := intercept(targetObj); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
}

// reconciler_test.go
var _ = Describe("Reconciler", func() {
	// Available in BeforeEach
	var (
		clientBuilder *fake.ClientBuilder
		scheme        *runtime.Scheme
	)

	// Available in JustBeforeEach
	var (
		cl  client.WithWatch
		rec *rvrstatusconfignodeid.Reconciler
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1alpha3.AddToScheme(scheme)).To(Succeed(), "should add v1alpha3 to scheme")
		clientBuilder = fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&v1alpha3.ReplicatedVolumeReplica{})
		cl = nil
		rec = nil
	})

	JustBeforeEach(func() {
		cl = clientBuilder.Build()
		rec = rvrstatusconfignodeid.NewReconciler(cl, GinkgoLogr)
	})

	It("returns no error when ReplicatedVolumeReplica does not exist", func(ctx SpecContext) {
		By("Reconciling non-existent ReplicatedVolumeReplica")
		Expect(rec.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "non-existent"},
		})).NotTo(Requeue(), "should ignore NotFound errors")
	})

	When("Get fails with non-NotFound error", func() {
		internalServerError := errors.New("internal server error")
		BeforeEach(func() {
			clientBuilder = clientBuilder.WithInterceptorFuncs(InterceptGet(func(_ *v1alpha3.ReplicatedVolumeReplica) error {
				return internalServerError
			}))
		})

		It("should fail if getting ReplicatedVolumeReplica failed with non-NotFound error", func(ctx SpecContext) {
			By("Reconciling with Get interceptor that returns error")
			Expect(rec.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "test-rvr"},
			})).Error().To(MatchError(internalServerError), "should return error when Get fails")
		})
	})

	When("RVR created", func() {
		// Base RVRs created in BeforeEach, can be modified in child tests
		var (
			rvr      *v1alpha3.ReplicatedVolumeReplica
			rvrList  []v1alpha3.ReplicatedVolumeReplica
			otherRVR *v1alpha3.ReplicatedVolumeReplica
		)

		BeforeEach(func() {
			// Base RVR for volume-1 - used in most tests
			rvr = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-1",
					NodeName:             "node-1",
				},
			}

			// Base RVR for volume-2 - used for isolation tests
			otherRVR = &v1alpha3.ReplicatedVolumeReplica{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rvr-vol2-1",
				},
				Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
					ReplicatedVolumeName: "volume-2",
					NodeName:             "node-3",
				},
			}

			// Initialize empty list - will be populated in child tests
			rvrList = nil
		})

		JustBeforeEach(func(ctx SpecContext) {
			if rvr != nil {
				Expect(cl.Create(ctx, rvr)).To(Succeed(), "should create base RVR")
			}
			// rvrList and otherRVR are created only in child tests when needed
		})

		DescribeTableSubtree("when rvr has",
			Entry("nil Status", func() { rvr.Status = nil }),
			Entry("nil Status.DRBD", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: nil} }),
			Entry("nil Status.DRBD.Config", func() { rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{DRBD: &v1alpha3.DRBD{Config: nil}} }),
			Entry("nil Status.DRBD.Config.NodeId", func() {
				rvr.Status = &v1alpha3.ReplicatedVolumeReplicaStatus{
					DRBD: &v1alpha3.DRBD{
						Config: &v1alpha3.DRBDConfig{NodeId: nil},
					},
				}
			}),
			func(setup func()) {
				BeforeEach(func() {
					setup()
				})

				It("should reconcile successfully and assign nodeID", func(ctx SpecContext) {
					By("Reconciling ReplicatedVolumeReplica with nil status fields")
					Expect(rec.Reconcile(ctx, RequestFor(rvr))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying nodeID was assigned")
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(rvr), updatedRVR)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID))), "first replica should get nodeID MinNodeID")
				})
			})

		When("multiple RVRs exist", func() {
			When("assigning nodeID sequentially", func() {
				var rvrList []v1alpha3.ReplicatedVolumeReplica

				BeforeEach(func() {
					rvrList = make([]v1alpha3.ReplicatedVolumeReplica, 6)
					for i := 0; i < 5; i++ {
						nodeID := uint(rvrstatusconfignodeid.MinNodeID + i)
						rvrList[i] = v1alpha3.ReplicatedVolumeReplica{
							ObjectMeta: metav1.ObjectMeta{
								Name: fmt.Sprintf("rvr-seq-%d", i+1),
							},
							Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
								ReplicatedVolumeName: "volume-1",
								NodeName:             fmt.Sprintf("node-%d", i+1),
							},
							Status: &v1alpha3.ReplicatedVolumeReplicaStatus{
								DRBD: &v1alpha3.DRBD{
									Config: &v1alpha3.DRBDConfig{NodeId: &nodeID},
								},
							},
						}
					}
					// Add one more without nodeId - should get next sequential nodeID
					rvrList[5] = v1alpha3.ReplicatedVolumeReplica{
						ObjectMeta: metav1.ObjectMeta{Name: "rvr-seq-6"},
						Spec: v1alpha3.ReplicatedVolumeReplicaSpec{
							ReplicatedVolumeName: "volume-1",
							NodeName:             "node-6",
						},
					}
				})

				JustBeforeEach(func(ctx SpecContext) {
					for i := range rvrList {
						Expect(cl.Create(ctx, &rvrList[i])).To(Succeed(), "should create RVR successfully")
					}
				})

				It("assigns nodeID sequentially and ensures uniqueness", func(ctx SpecContext) {
					By("Reconciling replica without nodeID")
					Expect(rec.Reconcile(ctx, RequestFor(&rvrList[5]))).ToNot(Requeue(), "should not requeue after successful assignment")

					By("Verifying sequential assignment: next available nodeID after 0-4")
					updatedRVR := &v1alpha3.ReplicatedVolumeReplica{}
					Expect(cl.Get(ctx, client.ObjectKeyFromObject(&rvrList[5]), updatedRVR)).To(Succeed(), "should get updated RVR")
					Expect(updatedRVR).To(HaveField("Status.DRBD.Config.NodeId", PointTo(BeNumerically("==", rvrstatusconfignodeid.MinNodeID+5))), "should assign nodeID MinNodeID+5 as next sequential value")
				})
			})
		})
	})
})
```

**Преимущества такой структуры:**
- Читаемость: вложенные `When` блоки логически группируют тесты
- Понимание: `By()` и второй аргумент в `Expect()` делают тесты самодокументируемыми
- Организация: легко найти тесты для конкретного сценария
- Масштабируемость: легко добавлять новые тесты в соответствующие группы
- Отладка: `By()` отображается в выводе тестов при запуске с `-ginkgo.v`, что упрощает понимание, на каком шаге тест упал
- Гибкость: базовые ресурсы в `BeforeEach` можно модифицировать в дочерних тестах перед созданием
- Изоляция: каждый тест создает только нужные ему ресурсы в `JustBeforeEach`

**Запуск тестов с выводом `By()`:**
```bash
# С выводом шагов By()
go test ./internal/controllers/my_controller/... -v -ginkgo.v

# С дополнительной информацией о нодах (BeforeEach, JustBeforeEach и т.д.)
go test ./internal/controllers/my_controller/... -v -ginkgo.v -ginkgo.show-node-events
```

---

## Ссылки

- [controller-runtime documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [Kubebuilder book](https://book.kubebuilder.io/)
- Спецификация контроллеров: `docs/dev/spec_v1alpha3.md`

