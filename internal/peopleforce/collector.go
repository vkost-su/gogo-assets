package peopleforce

import (
	"context"
	"time"

	"gogo-assets/internal/collector"
	"gogo-assets/internal/logging"
)

// Compile-time check that Collector satisfies the collector.Collector interface.
var _ collector.Collector = (*Collector)(nil)

// Collector orchestrates the PeopleForce collection pipeline:
//
//  1. list_asset_categories  — build id→name lookup
//  2. list_employees         — build user_id→Employee lookup
//  3. list_assets            — enrich with category name + current assignee
type Collector struct {
	client *Client
	out    *Output
	log    interface {
		Info(msg string, args ...any)
		Warn(msg string, args ...any)
	}
}

// NewCollector wraps a PeopleForce Client. out is the sink that CollectAll
// writes results into.
func NewCollector(c *Client, out *Output) *Collector {
	return &Collector{
		client: c,
		out:    out,
		log:    logging.For("peopleforce"),
	}
}

// Name returns the short collector identifier used in logs.
func (c *Collector) Name() string { return "peopleforce" }

// CollectAll runs the three-step pipeline and writes into c.out.
func (c *Collector) CollectAll(ctx context.Context) error {
	start := time.Now()

	c.log.Info("fetching asset categories")
	categories, err := c.buildCategoryMap(ctx)
	if err != nil {
		return err
	}
	c.log.Info("categories loaded", "count", len(categories))

	c.log.Info("fetching employees")
	employees, err := c.buildEmployeeMap(ctx)
	if err != nil {
		return err
	}
	c.log.Info("employees loaded", "count", len(employees))

	c.log.Info("fetching assets")
	rawAssets, err := c.client.ListAssets(ctx)
	if err != nil {
		return err
	}
	c.log.Info("assets fetched", "count", len(rawAssets))

	assets := make([]Asset, 0, len(rawAssets))
	unresolved := 0
	for _, raw := range rawAssets {
		a := MapAsset(raw)

		// Resolve category name.
		if name, ok := categories[a.CategoryID]; ok {
			a.CategoryName = name
		}

		// Find the active (non-returned) assignment and resolve the employee.
		if cur := currentAssignment(a.Assignments); cur != nil {
			a.IsAssigned = true
			a.AssignedToID = cur.UserID
			a.IssuedOn = cur.IssuedOn

			if emp, ok := employees[cur.UserID]; ok {
				a.AssignedEmail = emp.Email
				a.AssignedName = emp.FullName
				a.Department = emp.Department
				a.Position = emp.Position
				a.Location = emp.Location
			} else {
				unresolved++
			}
		}
		assets = append(assets, a)
	}

	if unresolved > 0 {
		c.log.Warn("assets with unresolved assignee", "count", unresolved)
	}

	c.log.Info("complete",
		"assets", len(assets),
		"assigned", countAssigned(assets),
		"elapsed", logging.Elapsed(start))

	c.out.Assets = assets
	c.out.Queries = c.client.Queries()
	return nil
}

func (c *Collector) buildCategoryMap(ctx context.Context) (map[string]string, error) {
	raw, err := c.client.ListAssetCategories(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(raw))
	for _, item := range raw {
		cat := MapAssetCategory(item)
		if cat.ID != "" {
			m[cat.ID] = cat.Name
		}
	}
	return m, nil
}

func (c *Collector) buildEmployeeMap(ctx context.Context) (map[int]*Employee, error) {
	raw, err := c.client.ListEmployees(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[int]*Employee, len(raw))
	for _, item := range raw {
		emp := MapEmployee(item)
		if emp.ID != 0 {
			emp := emp // avoid aliasing the loop variable
			m[emp.ID] = &emp
		}
	}
	return m, nil
}

func countAssigned(assets []Asset) int {
	n := 0
	for _, a := range assets {
		if a.IsAssigned {
			n++
		}
	}
	return n
}
