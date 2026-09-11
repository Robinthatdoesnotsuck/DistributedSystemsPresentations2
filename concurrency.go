
paradigma

y = 0
x = 0

func AsignData() {
	y = 2
	x = 1
}

// Message passing
//
//happened-before relationship

func atomicOperation() {

}

func main() {
	var raceConditionCounter int
	go func() {raceConditionCounter++}()
	go func() {raceConditionCounter++}()

	go atomic.AddInt64(&raceConditionCounter, 1)
	go atomic.AddInt64(&raceConditionCounter, 1)
}

