package console

import "math/rand/v2"

func RandomASCIIEmoticon() string {
	emoticons := [...]string{":-)", ":-(", ";-)", ":-/", ":-D", ":|", ":P", "^_^", "o_O", "O_o", "x_x"}

	return emoticons[rand.IntN(len(emoticons))]
}
