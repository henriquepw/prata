package web

import "math/rand/v2"

func randomASCIIEmoticon() string {
	emoticons := [...]string{":-)", ":-(", ";-)", ":-/", ":-D", ":|", ":P", "^_^", "o_O", "O_o", "x_x"}

	return emoticons[rand.IntN(len(emoticons))]
}
