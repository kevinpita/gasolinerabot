# Fuel search

Find low fuel prices near a location in Spain and compare the benefit of travelling to another station.

## Language

**Base location**:
The saved location used for price rankings and scheduled alerts.
_Avoid_: Current location, home

**Temporary location**:
A location used for one search without changing the base location.
_Avoid_: New base

**Ranking**:
Stations ordered by price for one fuel within the search radius, with distance used to break price ties.
_Avoid_: Recommendation

**Baseline station**:
The nearest eligible station used as the reference for a refuelling comparison.
_Avoid_: Cheapest station

**Net saving**:
The fuel-price saving relative to the baseline, minus the candidate station's calculated travel cost. The baseline has a net saving of zero by definition.
_Avoid_: Gross saving

**Direction filter**:
An approximate selection of stations towards a named destination.
_Avoid_: Route detour calculation

**Scheduled alert**:
A daily or weekly price report for the saved location and fuels, whether or not prices changed.
_Avoid_: Price-change notification
