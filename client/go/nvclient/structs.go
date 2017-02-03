package nvclient

/*******
 * Result classes for parsing xml and json
  *******/

type Result interface {
	result()
	ID() string
	SetID(id string)
}

func (r *ResultArray) result() {}
func (r *ResultMap) result()   {}
func (r *ResultValue) result() {}

type ResultArray struct {
	Array []Result
	Name  string
}

func (r *ResultArray) ID() string {
	return r.Name
}
func (r *ResultArray) SetID(id string) {
	r.Name = id
}

type ResultMap struct {
	Map   map[string]Result
	order []string
	Name  string
}

func (r *ResultMap) ID() string {
	return r.Name
}
func (r *ResultMap) SetID(id string) {
	r.Name = id
}
func (r *ResultMap) Add(key string, value Result) {
	if r.Map == nil {
		r.Map = make(map[string]Result, 0)
	}
	r.Map[key] = value

	found := false
	for _, k := range r.GetOrder() {
		if k == key {
			found = true
		}
	}
	if !found {
		r.order = append(r.GetOrder(), key)
	}
}
func (r *ResultMap) Get(key string) Result {
	return r.Map[key]
}

func (r *ResultMap) GetOrder() []string {
	if r.order == nil {
		r.order = make([]string, 0)
	}
	return r.order
}

type ResultValue struct {
	Value string
	Name  string
}

func (r *ResultValue) ID() string {
	return r.Name
}
func (r *ResultValue) SetID(id string) {
	r.Name = id
}

func Compare(r1, r2 Result) bool {
	switch t1 := r1.(type) {
	case *ResultMap:
		switch t2 := r2.(type) {
		case *ResultMap:
			if len(t1.GetOrder()) != len(t2.GetOrder()) {
				return false
			} else {
				if t1.Get("name") != nil {
					if Compare(t1.Get("name"), t2.Get("name")) {
						return true
					} else {
						return false
					}
				}
				for _, k1 := range t1.GetOrder() {
					i1 := t1.Get(k1)
					i2 := t2.Get(k1)
					if i2 == nil {
						return false
					} else if !Compare(i1, i2) {
						return false
					}
				}
				return true
			}
		default:
			return false
		}
	case *ResultArray:
		switch t2 := r2.(type) {
		case *ResultArray:
			if len(t1.Array) != len(t2.Array) {
				return false
			} else {
				for _, i1 := range t1.Array {
					found := false
					for _, i2 := range t2.Array {
						if Compare(i1, i2) {
							found = true
						}
					}
					if !found {
						return false
					}
				}
				return true
			}
		default:
			return false

		}
	case *ResultValue:
		switch t2 := r2.(type) {
		case *ResultValue:
			if t1.Value == t2.Value {
				return true
			}
		default:
			return false
		}
	}
	return false
}
