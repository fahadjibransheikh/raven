package message

import (
	"strings"
	"testing"
)

func TestExtractSignInCode(t *testing.T) {
	tests := []struct {
		name, subject, text, want string
	}{
		// Positives.
		{"digits after keyword", "Your verification code is 482913", "", "482913"},
		{"digits before keyword", "123456 is your Google verification code", "", "123456"},
		{"google prefix", "G-123456 is your Google verification code", "", "123456"},
		{"one-time passcode", "Use 4821 as your one-time passcode", "", "4821"},
		{"hyphen split", "Your code: 123-456", "", "123456"},
		{"space split with period", "Login code: 123 456.", "", "123456"},
		{"otp", "Your OTP is 8472.", "", "8472"},
		{"alphanumeric", "Security code: A1B2C3", "", "A1B2C3"},
		{"pin with comma", "Your PIN is 7391, do not share it", "", "7391"},
		{"dash then code", "Verification code - 5538 (valid 10 minutes)", "", "5538"},
		{"uppercase keyword", "VERIFICATION CODE: 90817265", "", "90817265"},
		{"sign-in", "Your sign-in code is 667788", "", "667788"},
		{"in body, multiline", "Your Acme sign-in", "Hi Sam,\n\nYour sign-in code is:\n\n654321\n\nIt expires in 10 minutes.", "654321"},
		{"body only", "Welcome", "Enter this code to verify your email: 3049", "3049"},
		{"subject wins over body", "Your code is 111222", "Your code is 333444", "111222"},
		{"falls back to body when subject has none", "Verify your email", "Your verification code is 333444.", "333444"},
		{"order word earlier but code keyword closer", "Order shipped", "Your order has shipped. Your verification code is 482913.", "482913"},
		{"confirmation code", "Your confirmation code is 123456", "", "123456"},

		// Prices, dates, times, years.
		{"price", "Your total is $12.99, use code at checkout", "", ""},
		{"price no cents", "Verify your $1299 payment", "", ""},
		{"thousands", "Your login at 1,234 locations", "", ""},
		{"year", "Your sign-in code expires 2026", "", ""},
		{"date", "Verify your booking on 10/19", "", ""},
		{"iso date", "Verify by 2026-10-19", "", ""},
		{"time", "Meeting at 12:30, please verify attendance", "", ""},
		{"ip address", "Login from 192.168.1.1, verify it was you", "", ""},

		// Phone numbers.
		{"phone hyphens", "Call 800-555-1234 for login help", "", ""},
		{"phone spaces", "Call 555 123 4567 if login fails", "", ""},
		{"phone in text", "Account help", "Need to verify? Call 1-800-555-1234 any time.", ""},

		// Order, invoice, tracking, reference numbers.
		{"order number", "Your order 482913 needs verification", "", ""},
		{"invoice", "Invoice #4821 verify payment", "", ""},
		{"invoice word", "Invoice 4821 code", "", ""},
		{"tracking long", "Tracking number 1Z999AA10123456784 verify delivery", "", ""},
		{"tracking short", "Verify tracking 482913", "", ""},
		{"reference", "Ref 482913 verification pending", "", ""},
		{"order code", "Your order code 482913 is ready", "", ""},

		// Zip, promo, other codes that are not sign-in codes.
		{"zip word", "Zip code 98101", "", ""},
		{"zip in address", "Verify your address: 123 Main St, Seattle, WA 98101", "", ""},
		{"promo code", "Promo code SAVE20 gets you 20% off", "", ""},
		{"error code", "Error code 5001 while you sign in", "", ""},

		// Nothing to find.
		{"no keyword", "Your order 482913 shipped", "", ""},
		{"keyword without digits", "Your verification code", "Please use the verification code", ""},
		{"too short", "Use code 123 to join", "", ""},
		{"too long", "Verification code 123456789", "", ""},
		{"letters only", "Your verification code is ABCDEF", "", ""},
		{"lowercase alnum", "Your verification code is ab12cd", "", ""},
		{"empty", "", "", ""},
		{"beyond text limit", "Hello", strings.Repeat("x ", 1100) + "verification code 482913", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractSignInCode(tt.subject, tt.text); got != tt.want {
				t.Fatalf("ExtractSignInCode(%q, %q) = %q, want %q", tt.subject, tt.text, got, tt.want)
			}
		})
	}
}
