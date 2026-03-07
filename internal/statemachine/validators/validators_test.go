package validators

import "testing"

// ==================== Name ====================

func TestName_Valid(t *testing.T) {
	cases := []string{"Juan", "María José", "ANDRÉS", "ñoño", "Üe"}
	for _, c := range cases {
		if !Name(c) {
			t.Errorf("Name(%q) = false, want true", c)
		}
	}
}

func TestName_Invalid(t *testing.T) {
	cases := []string{"", "A", "Juan123", "Hello!", "a@b", "1234567890", "A B C D E F G H I J K L M N O P Q R S T U V W X Y Z extra long name that exceeds fifty chars limit"}
	for _, c := range cases {
		if Name(c) {
			t.Errorf("Name(%q) = true, want false", c)
		}
	}
}

// ==================== NotEmpty ====================

func TestNotEmpty_Valid(t *testing.T) {
	cases := []string{"hello", " a ", "123"}
	for _, c := range cases {
		if !NotEmpty(c) {
			t.Errorf("NotEmpty(%q) = false, want true", c)
		}
	}
}

func TestNotEmpty_Invalid(t *testing.T) {
	cases := []string{"", "   ", "\t", "\n"}
	for _, c := range cases {
		if NotEmpty(c) {
			t.Errorf("NotEmpty(%q) = true, want false", c)
		}
	}
}

// ==================== Document ====================

func TestDocument_Valid(t *testing.T) {
	cases := []string{"12345", "1234567890", "123456789012345"}
	for _, c := range cases {
		if !Document(c) {
			t.Errorf("Document(%q) = false, want true", c)
		}
	}
}

func TestDocument_Invalid(t *testing.T) {
	cases := []string{"", "1234", "abcde", "123 456", "1234567890123456"}
	for _, c := range cases {
		if Document(c) {
			t.Errorf("Document(%q) = true, want false", c)
		}
	}
}

// ==================== Email ====================

func TestEmail_Valid(t *testing.T) {
	cases := []string{"user@example.com", "user.name+tag@domain.co", "a@b.cd"}
	for _, c := range cases {
		if !Email(c) {
			t.Errorf("Email(%q) = false, want true", c)
		}
	}
}

func TestEmail_Invalid(t *testing.T) {
	cases := []string{"", "user", "user@", "@domain.com", "user@.com", "user@domain"}
	for _, c := range cases {
		if Email(c) {
			t.Errorf("Email(%q) = true, want false", c)
		}
	}
}

// ==================== ColombianPhone ====================

func TestColombianPhone_Valid(t *testing.T) {
	cases := []string{"3001234567", "+573001234567", "573001234567"}
	for _, c := range cases {
		if !ColombianPhone(c) {
			t.Errorf("ColombianPhone(%q) = false, want true", c)
		}
	}
}

func TestColombianPhone_Invalid(t *testing.T) {
	cases := []string{"", "1234567890", "300123456", "abc", "12345"}
	for _, c := range cases {
		if ColombianPhone(c) {
			t.Errorf("ColombianPhone(%q) = true, want false", c)
		}
	}
}

// ==================== MinLength ====================

func TestMinLength(t *testing.T) {
	v := MinLength(3)
	if !v("abc") {
		t.Error("MinLength(3)(\"abc\") = false, want true")
	}
	if !v("abcd") {
		t.Error("MinLength(3)(\"abcd\") = false, want true")
	}
	if v("ab") {
		t.Error("MinLength(3)(\"ab\") = true, want false")
	}
	if v("  ") {
		t.Error("MinLength(3)(spaces) = true, want false")
	}
}

// ==================== NumRange ====================

func TestNumRange_Valid(t *testing.T) {
	v := NumRange(1, 10)
	for _, s := range []string{"1", "5", "10"} {
		if !v(s) {
			t.Errorf("NumRange(1,10)(%q) = false, want true", s)
		}
	}
}

func TestNumRange_Invalid(t *testing.T) {
	v := NumRange(1, 10)
	for _, s := range []string{"0", "11", "-1", "abc", ""} {
		if v(s) {
			t.Errorf("NumRange(1,10)(%q) = true, want false", s)
		}
	}
}

// ==================== FloatRange ====================

func TestFloatRange_Valid(t *testing.T) {
	v := FloatRange(10.0, 300.0)
	for _, s := range []string{"10", "150.5", "300", "10.0", "299.99"} {
		if !v(s) {
			t.Errorf("FloatRange(10,300)(%q) = false, want true", s)
		}
	}
}

func TestFloatRange_Invalid(t *testing.T) {
	v := FloatRange(10.0, 300.0)
	for _, s := range []string{"9.99", "300.01", "-1", "abc", ""} {
		if v(s) {
			t.Errorf("FloatRange(10,300)(%q) = true, want false", s)
		}
	}
}
