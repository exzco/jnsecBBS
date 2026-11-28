package service

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
