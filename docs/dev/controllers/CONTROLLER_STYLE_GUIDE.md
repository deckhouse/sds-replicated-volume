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
	WithEventFilter(predicate.Funcs{
		// фильтры событий
	}).
	Complete(rec)
```

---

## 3. Использование handlers для маппинга событий

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
WithEventFilter(predicate.Funcs{
	CreateFunc: func(ce event.CreateEvent) bool {
		obj := ce.Object.(*v1alpha3.SomeResource)
		// логика фильтрации
		return shouldProcess
	},
	UpdateFunc: func(_ event.UpdateEvent) bool {
		return false // если не нужно обрабатывать
	},
	DeleteFunc: func(_ event.DeleteEvent) bool {
		return false // если не нужно обрабатывать
	},
	GenericFunc: func(ge event.GenericEvent) bool {
		// для reconciliation на старте
		return shouldProcess
	},
})
```

---

## 4. Использовать structured logger

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
	log := r.logAlt.WithName("Reconcile").WithValues("req", req)
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

## 5. Упрощенная структура Reconciler

**⚠️ Не используйте без веской причины:**
- Неиспользуемые поля (`rdr client.Reader`, `sch *runtime.Scheme`)
- Неэкспортированные поля (затрудняют тестирование)
- Функции-конструкторы (`NewReconciler`) - создавайте напрямую в `controller.go`

**✅ Используйте по умолчанию:**
- Минимальная структура с только необходимыми полями: `Cl`, `Log`, `LogAlt`
- Экспортированные поля (с заглавной буквы) для использования в тестах
- Прямое создание структуры в `BuildController`

**Обоснование:** Простая структура легче понимать и поддерживать. Экспортированные поля позволяют создавать reconciler напрямую в тестах без необходимости в конструкторах. Неиспользуемые поля добавляют сложность без пользы.

**Пример:**
```go
// ✅ ПРАВИЛЬНО - минимальная структура
type Reconciler struct {
	Cl     client.Client
	Log    *slog.Logger
	LogAlt logr.Logger
}

// В controller.go
rec := &Reconciler{
	Cl:     mgr.GetClient(),
	Log:    slog.Default(),
	LogAlt: mgr.GetLogger(),
}

// В тестах
rec := &Reconciler{
	Cl:     cl,
	Log:    slog.Default(),
	LogAlt: logr.Discard(),
}

// ❌ НЕПРАВИЛЬНО - неиспользуемые поля
type Reconciler struct {
	cl     client.Client
	rdr    client.Reader  // не используется
	sch    *runtime.Scheme // не используется
	log    *slog.Logger
	logAlt logr.Logger
}
```

---

## 6. Обработка неиспользуемых параметров

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

## 7. Размещение констант (опционально)

**⚠️ Опционально:** Размещение констант в отдельном файле `consts.go`.

**✅ Рекомендуется:**
- Если контроллер использует несколько констант (2+), можно вынести их в отдельный файл `consts.go` в директории контроллера
- Это улучшает читаемость и организацию кода
- Константы должны быть связаны с логикой контроллера

**⚠️ Не обязательно:**
- Если константа одна или две, можно оставить их в `reconciler.go`
- Если константы используются только в одном месте, можно оставить их локально

**Обоснование:** Разделение констант в отдельный файл улучшает организацию кода, но не является обязательным требованием. Используйте по необходимости.

**Пример:**
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
- Экспортируйте поля для упрощения тестирования
- Избегайте функций-конструкторов - создавайте структуру напрямую

### Файлы контроллера:

1. **`controller.go`** - регистрация контроллера
   - `BuildController(mgr manager.Manager) error`
   - Настройка builder с `.For()`, `.WithEventFilter()`, `.Complete()`
   - Инициализация Reconciler с зависимостями

2. **`reconciler.go`** - логика reconcile
   - `Reconciler` struct с минимальными зависимостями (`client.Client`, `*slog.Logger`, `logr.Logger`)
   - Поля должны быть экспортированы (`Cl`, `Log`, `LogAlt`) для использования в тестах
   - `Reconcile(ctx, req reconcile.Request)` метод
   - Вспомогательные методы для бизнес-логики
   - **Важно:** Не добавляйте неиспользуемые поля (`rdr`, `sch`) - они добавляют сложность без пользы

3. **`consts.go`** (опционально) - константы контроллера
   - Константы, используемые в логике контроллера
   - Рекомендуется, если констант несколько (2+)
   - Не обязательно, если константа одна или две

4. **`reconciler_test.go`** - unit тесты
   - Тесты с fake Kubernetes client
   - Использовать `reconcile.Request{NamespacedName: types.NamespacedName{Name: "name"}}`
   - Покрытие основных сценариев и edge cases

---

## Пример полного контроллера

### controller.go
```go
package mycontroller

import (
	"log/slog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	u "github.com/deckhouse/sds-common-lib/utils"
)

func BuildController(mgr manager.Manager) error {
	rec := &Reconciler{
		Cl:     mgr.GetClient(),
		Log:    slog.Default(),
		LogAlt: mgr.GetLogger(),
	}

	err := builder.ControllerManagedBy(mgr).
		Named("my_controller").
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

	if err != nil {
		return u.LogError(rec.Log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
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
	"log/slog"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	"github.com/deckhouse/sds-replicated-volume/lib/go/common/api"
)

type Reconciler struct {
	Cl     client.Client
	Log    *slog.Logger
	LogAlt logr.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.LogAlt.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")

	obj := &v1alpha3.SomeResource{}
	if err := r.Cl.Get(ctx, req.NamespacedName, obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("resource not found, might be deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("getting resource %s: %w", req.NamespacedName, err)
	}

	// бизнес-логика
	// При обновлении status используйте PatchStatusWithConflictRetry:
	// objKey := client.ObjectKeyFromObject(obj)
	// freshObj := &v1alpha3.SomeResource{}
	// if err := r.Cl.Get(ctx, objKey, freshObj); err != nil {
	//     return reconcile.Result{}, fmt.Errorf("getting resource for patch: %w", err)
	// }
	// if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshObj, func(currentObj *v1alpha3.SomeResource) error {
	//     // Инициализация status внутри patchFn (не в начале Reconcile)
	//     if currentObj.Status == nil {
	//         currentObj.Status = &v1alpha3.SomeResourceStatus{}
	//     }
	//     // Обновление status
	//     currentObj.Status.SomeField = value
	//     return nil
	// }); err != nil {
	//     return reconcile.Result{}, fmt.Errorf("updating status: %w", err)
	// }

	log.Info("completed successfully")
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
		rec = &mycontroller.Reconciler{
			Cl:     cl,
			Log:    slog.Default(),
			LogAlt: GinkgoLogr,
		}
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
- [ ] Использовать `.For()` для основного ресурса
- [ ] Использовать `EnqueueRequestForOwner` если нужно реагировать на дочерние ресурсы с owner reference
- [ ] Использовать `EnqueueRequestsFromMapFunc` только если нет owner reference, но нужен маппинг
- [ ] Использовать structured logger с `.WithName()` и `.WithValues()` в Reconcile
- [ ] Использовать `predicate.Funcs` для фильтрации событий при необходимости
- [ ] **Упрощенная структура Reconciler:** только `Cl`, `Log`, `LogAlt` (без неиспользуемых полей)
- [ ] **Экспортированные поля:** `Cl`, `Log`, `LogAlt` для использования в тестах
- [ ] **Инициализация status:** внутри `patchFn`, а не в начале `Reconcile`
- [ ] **Использование PatchStatusWithConflictRetry:** для всех обновлений status
- [ ] **Идемпотентность:** проверка состояния в начале Reconcile и внутри patchFn
- [ ] Добавить unit тесты с **Ginkgo/Gomega**
- [ ] Использовать fake client с `.WithStatusSubresource()`
- [ ] Использовать `reconcile.Request` в тестах
- [ ] Зарегистрировать контроллер в `registry.go`

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

### Конфликты при обновлении
- **Всегда** используйте `api.PatchStatusWithConflictRetry` или `api.PatchWithConflictRetry` для безопасных обновлений
- Эти функции обрабатывают конфликты через optimistic locking
- **Важно:** Инициализация status должна происходить **внутри** `patchFn`, а не в начале `Reconcile`
- Это гарантирует, что инициализация происходит атомарно вместе с обновлением и предотвращает race conditions

**Пример:**
```go
// ❌ НЕПРАВИЛЬНО - инициализация в начале Reconcile
if obj.Status == nil {
    obj.Status = &v1alpha3.SomeResourceStatus{}
}
// ... обновление status

// ✅ ПРАВИЛЬНО - инициализация внутри patchFn
objKey := client.ObjectKeyFromObject(obj)
freshObj := &v1alpha3.SomeResource{}
if err := r.Cl.Get(ctx, objKey, freshObj); err != nil {
    return reconcile.Result{}, fmt.Errorf("getting resource for patch: %w", err)
}
if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshObj, func(currentObj *v1alpha3.SomeResource) error {
    // Инициализация status внутри patchFn
    if currentObj.Status == nil {
        currentObj.Status = &v1alpha3.SomeResourceStatus{}
    }
    // Обновление status
    currentObj.Status.SomeField = value
    return nil
}); err != nil {
    return reconcile.Result{}, fmt.Errorf("updating status: %w", err)
}
```

### Идемпотентность
- Reconcile должен быть идемпотентным
- Проверять текущее состояние перед изменениями (ранний выход, если уже установлено)
- Внутри `patchFn` также проверять состояние (на случай, если другой worker уже обновил)
- Не делать лишних обновлений, если состояние уже корректное

**Пример:**
```go
// Проверка в начале Reconcile
if obj.Status != nil && obj.Status.SomeField != nil {
    log.V(1).Info("field already set", "field", *obj.Status.SomeField)
    return reconcile.Result{}, nil
}

// Внутри patchFn - дополнительная проверка на случай race condition
if err := api.PatchStatusWithConflictRetry(ctx, r.Cl, freshObj, func(currentObj *v1alpha3.SomeResource) error {
    // Проверка еще раз (idempotent check)
    if currentObj.Status != nil && currentObj.Status.SomeField != nil {
        log.V(1).Info("field already set by another worker")
        return nil // Уже установлено, ничего не делаем
    }
    // Установка значения
    if currentObj.Status == nil {
        currentObj.Status = &v1alpha3.SomeResourceStatus{}
    }
    currentObj.Status.SomeField = &value
    return nil
}); err != nil {
    return reconcile.Result{}, fmt.Errorf("updating status: %w", err)
}
```

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

**Структура тестов:**
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
		rec = &mycontroller.Reconciler{
			Cl:     cl,
			Log:    slog.Default(),
			LogAlt: GinkgoLogr,
		}
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

**Покрытие:**
- Основные сценарии (happy path)
- Edge cases (ошибки, граничные условия)
- Идемпотентность

---

## Ссылки

- [controller-runtime documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [Kubebuilder book](https://book.kubebuilder.io/)
- Спецификация контроллеров: `docs/dev/spec_v1alpha3.md`

