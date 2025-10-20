package main

import (
	"fmt"
	"math"
	"strings"
)

func parseBytesFlag(s string) (uint64, error) {
	// Accept values like "5GB", "500M", "1024", etc.
	s = trimSpaceUpper(s)
	mult := uint64(1)
	switch {
	case hasSuffix(s, "GB"):
		mult = 1024 * 1024 * 1024
		s = trimSuffix(s, "GB")
	case hasSuffix(s, "G"):
		mult = 1000 * 1000 * 1000
		s = trimSuffix(s, "G")
	case hasSuffix(s, "MB"):
		mult = 1024 * 1024
		s = trimSuffix(s, "MB")
	case hasSuffix(s, "M"):
		mult = 1000 * 1000
		s = trimSuffix(s, "M")
	case hasSuffix(s, "KB"):
		mult = 1024
		s = trimSuffix(s, "KB")
	case hasSuffix(s, "K"):
		mult = 1000
		s = trimSuffix(s, "K")
	}
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	if err != nil {
		return 0, err
	}
	return uint64(v * float64(mult)), nil
}

func deltaSnap(oldSnap, newSnap snapshot) map[snapKey]digestStat {
	out := make(map[snapKey]digestStat)
	for k, newv := range newSnap {
		if oldv, ok := oldSnap[k]; ok {
			// compute delta of counts/rows
			dCount := uint64(0)
			if newv.CountStar >= oldv.CountStar {
				dCount = newv.CountStar - oldv.CountStar
			}
			dRowsExam := uint64(0)
			if newv.SumRowsExam >= oldv.SumRowsExam {
				dRowsExam = newv.SumRowsExam - oldv.SumRowsExam
			}
			dRowsSent := uint64(0)
			if newv.SumRowsSent >= oldv.SumRowsSent {
				dRowsSent = newv.SumRowsSent - oldv.SumRowsSent
			}
			if dCount == 0 && dRowsExam == 0 && dRowsSent == 0 {
				continue
			}
			out[k] = digestStat{
				Digest:      newv.Digest,
				DigestText:  newv.DigestText,
				CountStar:   dCount,
				SumRowsExam: dRowsExam,
				SumRowsSent: dRowsSent,
			}
		} else {
			// new digest, treat entire counts as delta
			out[k] = digestStat{
				Digest:      newv.Digest,
				DigestText:  newv.DigestText,
				CountStar:   newv.CountStar,
				SumRowsExam: newv.SumRowsExam,
				SumRowsSent: newv.SumRowsSent,
			}
		}
	}
	return out
}

func bytesToHuman(b uint64) string {
	if b == 0 {
		return "0B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(b) / float64(div)
	prefix := []string{"KiB", "MiB", "GiB", "TiB"}[exp]
	return fmt.Sprintf("%.2f%s", value, prefix)
}

func trimString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Small local helpers to avoid pulling strings into this file; monitor code already imports strings
func trimSpaceUpper(s string) string  { return strings.ToUpper(strings.TrimSpace(s)) }
func hasSuffix(s, suf string) bool    { return strings.HasSuffix(s, suf) }
func trimSuffix(s, suf string) string { return strings.TrimSuffix(s, suf) }

// Rank helper
func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// Sorting key by max read/write bytes
func lessByMaxRW(a, b offender) bool {
	ma := math.Max(float64(a.BytesRead), float64(a.BytesWrite))
	mb := math.Max(float64(b.BytesRead), float64(b.BytesWrite))
	return ma > mb
}
