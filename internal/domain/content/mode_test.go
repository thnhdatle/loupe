// SPDX-License-Identifier: Apache-2.0

package content_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/content"
)

func TestMode_ParseMode_Table(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    content.Mode
		wantErr bool
	}{
		{name: "off", in: "off", want: content.ModeOff},
		{name: "on", in: "on", want: content.ModeOn},
		{name: "metadata only", in: "metadata_only", want: content.ModeMetadataOnly},
		{name: "unknown", in: "all", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "case sensitive", in: "ON", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := content.ParseMode(tc.in)
			if tc.wantErr {
				assert.ErrorIs(t, err, content.ErrInvalidMode)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
