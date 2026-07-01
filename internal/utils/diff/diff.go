package diff

type Elem interface {
	~string | ~byte | ~int | ~int8 | ~int16 | ~int64 | ~float32 | ~float64
}

func Diff[T Elem](old []T, new []T) (deleted []T, added []T) {
	m := make(map[T]int, len(old))
	for _, v := range old {
		m[v]++
	}
	for _, v := range new {
		if m[v] > 0 {
			m[v]--
		} else {
			added = append(added, v)
		}
	}
	for k, c := range m {
		for i := 0; i < c; i++ {
			deleted = append(deleted, k)
		}
	}
	return
}
