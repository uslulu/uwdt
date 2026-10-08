package backend

// Этап подключения для главного экрана — по сообщениям ядра, чтобы не лезть в логи.
// Этапы идут только вперёд; капча и пауза ВК показываются поверх, пока туннель не поднят.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var reWorkersTotal = regexp.MustCompile(`зарегистрирован \(всего: (\d+)\)`)

type stageTracker struct {
	rank     int
	channels int
	last     string
}

const stageRankChannels = 4

// feed возвращает новую строку этапа, если сообщение двигает подключение вперёд.
func (t *stageTracker) feed(msg string) (string, bool) {
	var text string
	rank := 0
	special := false
	switch {
	case strings.Contains(msg, "global lockout") || strings.Contains(msg, "CAPTCHA_WAIT_REQUIRED"):
		text, special = "ВКонтакте просит подождать — повторяю попытку…", true
	case strings.Contains(msg, "[КАПЧА]"):
		text, special = "ВКонтакте проверяет, не робот ли — решаю капчу…", true
	case strings.Contains(msg, "Запрос кредов"):
		text, rank = "Получаю доступ к звонку ВК…", 1
	case strings.Contains(msg, "Креды OK"):
		text, rank = "Доступ получен, подключаюсь к серверам ВК…", 2
	case strings.Contains(msg, "Relay:") || strings.Contains(msg, "Рукопожатие"):
		text, rank = "Соединяюсь с серверами ВК…", 3
	case reWorkersTotal.MatchString(msg):
		n, _ := strconv.Atoi(reWorkersTotal.FindStringSubmatch(msg)[1])
		if n <= t.channels {
			return "", false
		}
		t.channels = n
		text, rank = fmt.Sprintf("Поднято каналов: %d", n), stageRankChannels
	case strings.Contains(msg, "Запрос прав администратора"):
		text, rank = "Подтвердите пароль администратора…", 5
	case strings.Contains(msg, "Туннель") && strings.Contains(msg, "поднят"):
		text, rank = "Поднимаю туннель…", 6
	default:
		return "", false
	}

	if special {
		if t.rank > stageRankChannels {
			return "", false // туннель уже работает — вторичные группы не важны
		}
	} else if rank < t.rank || (rank == t.rank && rank != stageRankChannels) {
		return "", false
	} else {
		t.rank = rank
	}
	if text == t.last {
		return "", false
	}
	t.last = text
	return text, true
}

// StageSequence — для тестов: какие этапы увидит пользователь на этих строках лога.
func StageSequence(logs []string) []string {
	var t stageTracker
	var out []string
	for _, l := range logs {
		if s, ok := t.feed(l); ok {
			out = append(out, s)
		}
	}
	return out
}
