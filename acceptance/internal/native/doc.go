// SPDX-License-Identifier: Apache-2.0

// Package native builds the loupe binary from the working tree and runs it as a
// child process for the acceptance tests that need raw file access or kill -9
// (LPE-07, LPE-10).
package native
