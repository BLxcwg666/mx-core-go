package post

import (
	"reflect"
	"testing"
)

func TestPostListOrdersDefault(t *testing.T) {
	got, needsCategoryJoin := postListOrders(ListQuery{})
	want := []string{
		"COALESCE(pin_order, 0) DESC",
		"created_at DESC",
	}

	if needsCategoryJoin {
		t.Fatal("postListOrders() needsCategoryJoin = true, want false")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postListOrders() = %v, want %v", got, want)
	}
}

func TestPostListOrdersForPinOrderDesc(t *testing.T) {
	sortBy := "pinOrder"
	lq := ListQuery{SortBy: &sortBy}

	got, needsCategoryJoin := postListOrders(lq)
	want := []string{"COALESCE(pin_order, 0) DESC"}

	if needsCategoryJoin {
		t.Fatal("postListOrders() needsCategoryJoin = true, want false")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postListOrders() = %v, want %v", got, want)
	}
}

func TestPostListOrdersForModifiedDesc(t *testing.T) {
	sortBy := "modified"
	lq := ListQuery{SortBy: &sortBy}

	got, needsCategoryJoin := postListOrders(lq)
	want := []string{
		"CASE WHEN updated_at > created_at THEN 0 ELSE 1 END ASC",
		"CASE WHEN updated_at > created_at THEN updated_at END DESC",
		"created_at DESC",
	}

	if needsCategoryJoin {
		t.Fatal("postListOrders() needsCategoryJoin = true, want false")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postListOrders() = %v, want %v", got, want)
	}
}

func TestPostListOrdersForModifiedAsc(t *testing.T) {
	sortBy := "modified"
	sortOrder := 1
	lq := ListQuery{SortBy: &sortBy, SortOrder: &sortOrder}

	got, needsCategoryJoin := postListOrders(lq)
	want := []string{
		"CASE WHEN updated_at > created_at THEN 1 ELSE 0 END ASC",
		"CASE WHEN updated_at > created_at THEN updated_at END ASC",
		"created_at ASC",
	}

	if needsCategoryJoin {
		t.Fatal("postListOrders() needsCategoryJoin = true, want false")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("postListOrders() = %v, want %v", got, want)
	}
}
