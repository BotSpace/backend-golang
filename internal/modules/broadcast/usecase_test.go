package broadcast

import "testing"

func TestBuildKeyboardInline(t *testing.T) {
	raw := []byte(`[[{"title":"Sayt","link":"https://x.uz"},{"title":"Menu","callback_data":"menu"}]]`)
	kb := buildKeyboard("inline", raw)
	if kb == nil || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("inline keyboard noto'g'ri: %+v", kb)
	}
	if kb.InlineKeyboard[0][0].URL != "https://x.uz" || kb.InlineKeyboard[0][1].CallbackData != "menu" {
		t.Fatalf("tugma maydonlari noto'g'ri: %+v", kb.InlineKeyboard[0])
	}
}

func TestBuildKeyboardReply(t *testing.T) {
	raw := []byte(`[[{"title":"Kontakt","request_contact":true}]]`)
	kb := buildKeyboard("reply", raw)
	if kb == nil || !kb.ResizeKeyboard || !kb.Keyboard[0][0].RequestContact {
		t.Fatalf("reply keyboard noto'g'ri: %+v", kb)
	}
}

func TestBuildKeyboardEmpty(t *testing.T) {
	if kb := buildKeyboard("inline", []byte(`[]`)); kb != nil {
		t.Fatalf("bo'sh buttons uchun nil kutilgan edi: %+v", kb)
	}
}

func TestChatIDOf(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int64
		ok   bool
	}{
		{int64(42), 42, true},
		{int32(7), 7, true},
		{float64(99), 99, true},
		{"bad", 0, false},
		{nil, 0, false},
	} {
		got, ok := chatIDOf(map[string]any{"chat_id": tc.in})
		if got != tc.want || ok != tc.ok {
			t.Fatalf("chatIDOf(%v) = %d,%v; kutilgan %d,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
