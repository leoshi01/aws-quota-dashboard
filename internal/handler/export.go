package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuxishi/aws-quota-dashboard/internal/model"
)

func (h *Handler) ExportJSON(c *gin.Context) {
	regionParam := c.Query("region")
	serviceFilter := c.Query("service")

	cacheKey := "quotas:" + regionParam + ":" + serviceFilter
	var quotas []model.Quota

	if cached, ok := h.cache.Get(cacheKey); ok {
		if quotas, ok = cached.([]model.Quota); !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": errInvalidCacheDataType,
			})
			return
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "No data available. Please fetch quotas first.",
		})
		return
	}

	filename := fmt.Sprintf("aws-quotas-%s.json", time.Now().Format("2006-01-02"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.JSON(http.StatusOK, model.QuotaResponse{
		Quotas:    quotas,
		Total:     len(quotas),
		FetchedAt: time.Now(),
		FromCache: true,
	})
}

func (h *Handler) ExportHTML(c *gin.Context) {
	regionParam := c.Query("region")
	serviceFilter := c.Query("service")

	cacheKey := "quotas:" + regionParam + ":" + serviceFilter
	var quotas []model.Quota

	if cached, ok := h.cache.Get(cacheKey); ok {
		if quotas, ok = cached.([]model.Quota); !ok {
			c.String(http.StatusInternalServerError, errInvalidCacheDataType)
			return
		}
	} else {
		c.String(http.StatusBadRequest, "No data available. Please fetch quotas first.")
		return
	}

	html := generateHTMLReport(quotas)
	filename := fmt.Sprintf("aws-quotas-%s.html", time.Now().Format("2006-01-02"))
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, html)
}

func generateHTMLReport(quotas []model.Quota) string {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>AWS Quota Report</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; margin: 20px; }
        h1 { color: #232f3e; }
        table { border-collapse: collapse; width: 100%; margin-top: 20px; }
        th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
        th { background-color: #232f3e; color: white; }
        tr:nth-child(even) { background-color: #f2f2f2; }
        tr:hover { background-color: #ddd; }
        .timestamp { color: #666; font-size: 0.9em; }
    </style>
</head>
<body>
    <h1>AWS Service Quotas Report</h1>
    <p class="timestamp">Generated: ` + time.Now().Format("2006-01-02 15:04:05") + `</p>
    <p>Total quotas: ` + fmt.Sprintf("%d", len(quotas)) + `</p>
    <table>
        <thead>
            <tr>
                <th>Region</th>
                <th>Service</th>
                <th>Quota Name</th>
                <th>Value</th>
                <th>Unit</th>
                <th>Adjustable</th>
            </tr>
        </thead>
        <tbody>`

	for _, q := range quotas {
		adjustable := "No"
		if q.Adjustable {
			adjustable = "Yes"
		}
		html += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>%s</td>
                <td>%s</td>
                <td>%.0f</td>
                <td>%s</td>
                <td>%s</td>
            </tr>`, q.Region, q.ServiceName, q.QuotaName, q.Value, q.Unit, adjustable)
	}

	html += `
        </tbody>
    </table>
</body>
</html>`

	return html
}
