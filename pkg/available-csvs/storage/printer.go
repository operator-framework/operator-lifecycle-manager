package storage

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1beta1 "k8s.io/apimachinery/pkg/apis/meta/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/duration"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/printers"
)

// translateTimestampSince returns the elapsed time since timestamp in
// human-readable approximation.
func translateTimestampSince(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	return duration.HumanDuration(time.Since(timestamp.Time))
}

func addTableHandlers(h printers.PrintHandler) {
	podColumnDefinitions := []metav1beta1.TableColumnDefinition{
		{Name: "Name", Type: "string", Format: "name", Description: metav1.ObjectMeta{}.SwaggerDoc()["name"]},
		{Name: "Namespace", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["namespace"]},
		{Name: "Age", Type: "string", Description: metav1.ObjectMeta{}.SwaggerDoc()["creationTimestamp"]},
	}
	h.TableHandler(podColumnDefinitions, printAvailableCSV)
	h.TableHandler(podColumnDefinitions, printAvailableCSVList)
}

func printAvailableCSV(manifest *available.AvailableClusterServiceVersion, options printers.GenerateOptions) ([]metav1beta1.TableRow, error) {
	row := metav1beta1.TableRow{
		Object: runtime.RawExtension{Object: manifest},
	}
	row.Cells = append(row.Cells, manifest.Name, manifest.Namespace, translateTimestampSince(manifest.CreationTimestamp))
	return []metav1beta1.TableRow{row}, nil
}

func printAvailableCSVList(manifestList *available.AvailableClusterServiceVersionList, options printers.GenerateOptions) ([]metav1beta1.TableRow, error) {
	rows := make([]metav1beta1.TableRow, 0, len(manifestList.Items))
	for i := range manifestList.Items {
		r, err := printAvailableCSV(&manifestList.Items[i], options)
		if err != nil {
			return nil, err
		}
		rows = append(rows, r...)
	}
	return rows, nil
}
