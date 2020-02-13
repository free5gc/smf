package smf_context

import "fmt"

type IDQueue struct {
	QueueType IDType
	Queue     interface{}
}

type IDType int

const (
	PDRType  IDType = 0
	FARType  IDType = 1
	BARType  IDType = 2
	TEIDType IDType = 3
)

func NewIDQueue(idType IDType) (idQueue *IDQueue) {

	idQueue = &IDQueue{
		QueueType: idType,
	}

	switch idQueue.QueueType {
	case PDRType:
		q := make([]uint16, 0)
		idQueue.Queue = q
	case FARType:
		q := make([]uint32, 0)
		idQueue.Queue = q
	case BARType:
		q := make([]uint8, 0)
		idQueue.Queue = q
	}

	return
}

func (q IDQueue) Push(item int) {
	switch q.QueueType {
	case PDRType:
		q.Queue = append(q.Queue.([]uint16), uint16(item))
	case FARType:
		q.Queue = append(q.Queue.([]uint32), uint32(item))
	case BARType:
		q.Queue = append(q.Queue.([]uint8), uint8(item))

	}
}

func (q IDQueue) Pop() (id int, err error) {

	id = -1
	err = nil

	if !q.IsEmpty() {
		switch q.QueueType {
		case PDRType:
			pdr_queue := q.Queue.([]uint16)
			id = int(pdr_queue[0])
			pdr_queue = pdr_queue[1:]
		case FARType:
			far_queue := q.Queue.([]uint32)
			id = int(far_queue[0])
			far_queue = far_queue[1:]
		case BARType:
			bar_queue := q.Queue.([]uint8)
			id = int(bar_queue[0])
			bar_queue = bar_queue[1:]

		}
	} else {
		err = fmt.Errorf("Can't pop from empty id queue")
	}

	return
}

func (q IDQueue) IsEmpty() (isEmpty bool) {
	switch q.QueueType {
	case PDRType:
		pdr_queue := q.Queue.([]uint16)
		isEmpty = (len(pdr_queue) == 0)
	case FARType:
		far_queue := q.Queue.([]uint32)
		isEmpty = (len(far_queue) == 0)
	case BARType:
		bar_queue := q.Queue.([]uint8)
		isEmpty = (len(bar_queue) == 0)

	}

	return
}
