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
			name: "georgian balance notice",
			text: "Tqveni nomeri 555123456: gadaixade 5 lari 25-mde, gmadlobt",
			want: true,
		},
		{
			// Real SMS received on a SIMCOM modem: acronyms (MTS, SMS, VPN) and the
			// URL used to push the score over the threshold.
			name: "english carrier sms with acronyms and url",
			text: "MTS: Welcome to the MTS network! For the number 7747***7094 Internet and SMS are blocked for 24 hours. To unblock it, turn off Wi-Fi, VPN and pass verification using http://visit.mts.ru or by phone +79150102243",
			want: false,
		},
		{
			name: "english with domain only",
			text: "Top up via magti.ge or www.cellfie.ge, SMS 1234 for help",
			want: false,
		},
		{
			name: "english bank otp",
			text: "TBC Bank: transaction GEL 45.20 at SPAR. Available balance GEL 1203.55. Code 8841",
			want: false,
		},
		{
			name: "german sentence with consonant clusters",
			text: "Sehr geehrter Kunde, Ihr Vertrag wurde erfolgreich verlaengert. Freundliche Gruesse",
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
