package translator

import "testing"

func TestLooksLikeGeorgianLatin(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "real cellfie sms",
			text: `Momsaxureba "Nomris shenarchuneba" avtomaturad gagrzeldeba xval. Tu gsurs rom ganaxldes - iqonie balansze 0.1 lari, tu ara - gamorte vadis gasvlamde *110*9110#`,
			want: true,
		},
		{
			name: "short greeting",
			text: "gamarjoba, rogor xar?",
			want: true,
		},
		{
			name: "english sentence",
			text: "Your verification code is 483920. Do not share it with anyone.",
			want: false,
		},
		{
			name: "russian cyrillic",
			text: "Ваш баланс пополнен на 10 лари, спасибо за использование наших услуг.",
			want: false,
		},
		{
			name: "empty",
			text: "",
			want: false,
		},
		{
			name: "digits and symbols only",
			text: "*110*9110# 0.1",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := LooksLikeGeorgianLatin(tc.text); got != tc.want {
				t.Errorf("LooksLikeGeorgianLatin(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
