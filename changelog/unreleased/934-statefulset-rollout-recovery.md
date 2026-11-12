# Recover a failed server after a corrected rollout

The operator can replace an obsolete game pod stuck during startup once the StatefulSet has observed a newer revision. Recovery respects lifecycle operations and PodDisruptionBudgets.
