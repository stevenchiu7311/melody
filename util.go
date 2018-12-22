package melody

func indexFor(h int64, length int) int {
	if length > 1 {
		return int((h / 100) % (int64(length) - 1))
	}
	return length - 1
}
