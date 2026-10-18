package main

import (
	"bytes"
	"testing"
)

func logf(s string) {}

// buildTestFRU 构造一个只含 Board 区的合法 FRU
func buildTestFRU() []byte {
	hdr := []byte{0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	hdr[7] = fruChecksum(hdr, 7)

	board := make([]byte, 32)
	board[0] = 0x01 // version
	board[1] = 0x04 // 4 * 8 = 32 bytes
	board[2] = 0x00 // language
	// mfg date 3 字节保持 0
	i := 6
	board[i] = 0xC0 | 4
	copy(board[i+1:], "ACME")
	i += 5
	board[i] = 0xC0 | 3
	copy(board[i+1:], "PN1")
	i += 4
	board[i] = 0xC0 | 3
	copy(board[i+1:], "SN1")
	i += 4
	board[i] = 0xC0 // end of fields
	board[31] = fruChecksum(board, 31)

	return append(hdr, board...)
}

func TestApplyFRUFieldsChangesValue(t *testing.T) {
	src := buildTestFRU()
	dst := applyFRUFields(src, map[string]string{"Board_SerialNumber": "SN2"}, logf)

	if !bytes.Contains(dst, []byte("SN2")) {
		t.Fatalf("序列号未写入: %x", dst)
	}
	// Board 区校验和必须归零
	if sum := checksum(dst[8:40]); sum != 0 {
		t.Fatalf("board checksum 无效: %d", sum)
	}
	// Common header 校验和也必须有效
	if sum := checksum(dst[0:8]); sum != 0 {
		t.Fatalf("header checksum 无效: %d", sum)
	}
	// 未修改的字段保持原样
	if !bytes.Contains(dst, []byte("ACME")) || !bytes.Contains(dst, []byte("PN1")) {
		t.Fatal("其他字段被破坏")
	}
}

func TestApplyFRUFieldsTruncatesLongValue(t *testing.T) {
	src := buildTestFRU()
	dst := applyFRUFields(src, map[string]string{"Board_Manufacturer": "TOOLONGVALUE"}, logf)
	if sum := checksum(dst[0:8]); sum != 0 {
		t.Fatal("header checksum 无效")
	}
	if sum := checksum(dst[8:40]); sum != 0 {
		t.Fatal("board checksum 无效")
	}
	// 字段长度 4，只能写入前 4 个字符
	if !bytes.Contains(dst, []byte("TOOL")) {
		t.Fatalf("截断结果异常: %q", dst[14:20])
	}
}

// 关键回归：数据被截断（区域长度大于实际缓冲）时不能 panic
func TestApplyFRUFieldsTruncatedDoesNotPanic(t *testing.T) {
	src := buildTestFRU()
	truncated := src[:16] // board 区只剩 8 字节，但声明长度 32
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("applyFRUFields panic: %v", r)
		}
	}()
	dst := applyFRUFields(truncated, map[string]string{"Board_SerialNumber": "SN9"}, logf)
	if len(dst) != len(truncated) {
		t.Fatalf("长度变化: %d", len(dst))
	}
}

func TestApplyFRUFieldsBadOffsetsDoNotPanic(t *testing.T) {
	cases := [][]byte{
		{},                                   // 空数据
		{0x01},                               // 不足 8 字节
		{0x01, 0, 0, 0xFF, 0xFF, 0xFF, 0, 0}, // 偏移全部越界
		// board 偏移有效、但区域长度字节为 0 → 老代码里 end=off，
		// dst[off+6:end-1] 即 dst[14:7]，切片越界 panic
		{0x01, 0, 0, 0x01, 0, 0, 0, 0, 0x01, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		// chassis 区声明长度 0 的同类情况
		{0x01, 0, 0, 0x00, 0, 0, 0, 0, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	for i, c := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d panic: %v", i, r)
				}
			}()
			applyFRUFields(c, map[string]string{"Board_SerialNumber": "X"}, logf)
		}()
	}
}

func checksum(b []byte) int {
	sum := 0
	for _, x := range b {
		sum += int(x)
	}
	return sum % 256
}
