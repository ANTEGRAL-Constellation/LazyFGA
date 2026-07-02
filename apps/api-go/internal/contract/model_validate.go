package contract

import "strconv"

// validateModelIR 포트(§4.3 규칙 1~8 + 조건 참조). 예외 없이 위반을 모두 수집한다
// (빈 슬라이스 = 유효). code/path/message는 TS와 바이트 동일하며 수집 순서도 동일하다.

func subjectGroup(ref SubjectRef) (string, bool) {
	if ref.Kind == "group" {
		return ref.Group, true
	}
	return "", false
}

// ValidateModelIR는 IR을 정적 검증한다.
func ValidateModelIR(ir *ModelIR) []ValidationError {
	var errs []ValidationError
	add := func(code, path, message string) {
		errs = append(errs, ValidationError{Code: code, Path: path, Message: message})
	}

	// rule 1: 이름 식별자 규칙 + 예약어 충돌(CEL 예약어는 여기서 검사하지 않음 — 조건 이름만).
	checkName := func(name, path string) {
		if !isIdent(name) || isReservedWord(name) {
			add("BAD_NAME", path, `invalid identifier: "`+name+`"`)
		}
	}

	// rule 2: type 이름 전역 유일 + user 예약.
	seenType := make(map[string]struct{})
	checkTypeName := func(name, path string) {
		if name == "user" {
			add("RESERVED_USER", path, `"user" is a reserved base type and cannot be redefined`)
		} else {
			checkName(name, path)
		}
		if _, ok := seenType[name]; ok {
			add("DUP_TYPE", path, `duplicate type name: "`+name+`"`)
		}
		seenType[name] = struct{}{}
	}

	for gi, g := range ir.Groups {
		checkTypeName(g.Name, "groups["+strconv.Itoa(gi)+"].name")
	}
	for ri, r := range ir.Resources {
		checkTypeName(r.Name, "resources["+strconv.Itoa(ri)+"].name")
	}

	groupNameSet := make(map[string]struct{}, len(ir.Groups))
	for _, g := range ir.Groups {
		groupNameSet[g.Name] = struct{}{}
	}
	resourceNameSet := make(map[string]struct{}, len(ir.Resources))
	for _, r := range ir.Resources {
		resourceNameSet[r.Name] = struct{}{}
	}
	// 리소스명 → 보유 permission 이름 집합(rule 7 검사용).
	resourcePerms := make(map[string]map[string]struct{}, len(ir.Resources))
	for _, r := range ir.Resources {
		ps := make(map[string]struct{}, len(r.Permissions))
		for _, p := range r.Permissions {
			ps[p.Name] = struct{}{}
		}
		resourcePerms[r.Name] = ps
	}

	// rule 6: 그룹 멤버 type restriction의 group 참조 존재 + 빈 목록 금지.
	for gi, g := range ir.Groups {
		gp := "groups[" + strconv.Itoa(gi) + "]"
		if len(g.MemberTypes) == 0 {
			add("EMPTY_SUBJECTS", gp+".memberTypes", "group member list must be non-empty")
		}
		for mi, ref := range g.MemberTypes {
			if grp, ok := subjectGroup(ref); ok {
				if _, known := groupNameSet[grp]; !known {
					add("UNKNOWN_GROUP", gp+".memberTypes["+strconv.Itoa(mi)+"].group", `unknown group: "`+grp+`"`)
				}
			}
		}
	}

	for ri, r := range ir.Resources {
		base := "resources[" + strconv.Itoa(ri) + "]"
		roleNameSet := make(map[string]struct{}, len(r.Roles))
		for _, role := range r.Roles {
			roleNameSet[role.Name] = struct{}{}
		}

		// rule 3: relation 네임스페이스(role | can_<perm> | parent.relationName) 전역 유일.
		relationNamespace := make(map[string]string) // effectiveName -> origin path
		claimRelation := func(effectiveName, path string) {
			if origin, ok := relationNamespace[effectiveName]; ok {
				add("DUP_RELATION", path, `relation name "`+effectiveName+`" collides with `+origin)
			} else {
				relationNamespace[effectiveName] = path
			}
		}

		// parents: rule 8 + rule 4.
		parentRelSeen := make(map[string]struct{})
		for pi, p := range r.Parents {
			ppath := base + ".parents[" + strconv.Itoa(pi) + "]"
			checkName(p.RelationName, ppath+".relationName")
			if _, ok := parentRelSeen[p.RelationName]; ok {
				add("DUP_PARENT_RELATION", ppath+".relationName",
					`duplicate parent relation: "`+p.RelationName+`" (merge into one ParentRef)`)
			} else {
				parentRelSeen[p.RelationName] = struct{}{}
				claimRelation(p.RelationName, ppath+".relationName")
			}

			if len(p.ParentTypes) == 0 {
				add("UNKNOWN_PARENT", ppath+".parentTypes", "parentTypes must be non-empty")
			}
			seenPt := make(map[string]struct{})
			for pti, pt := range p.ParentTypes {
				if _, ok := seenPt[pt]; ok {
					add("DUP_PARENT_RELATION", ppath+".parentTypes["+strconv.Itoa(pti)+"]",
						`duplicate parent type "`+pt+`" in relation "`+p.RelationName+`"`)
				}
				seenPt[pt] = struct{}{}
				if _, ok := resourceNameSet[pt]; !ok {
					add("UNKNOWN_PARENT", ppath+".parentTypes["+strconv.Itoa(pti)+"]", `unknown parent type: "`+pt+`"`)
				}
			}
		}

		// roles: rule 1 + namespace + rule 6.
		for roi, role := range r.Roles {
			rolePath := base + ".roles[" + strconv.Itoa(roi) + "]"
			checkName(role.Name, rolePath+".name")
			claimRelation(role.Name, rolePath+".name")
			if len(role.AssignableBy) == 0 {
				add("EMPTY_SUBJECTS", rolePath+".assignableBy",
					`role "`+role.Name+`" must be assignable by >= 1 subject`)
			}
			for ai, ref := range role.AssignableBy {
				if grp, ok := subjectGroup(ref); ok {
					if _, known := groupNameSet[grp]; !known {
						add("UNKNOWN_GROUP", rolePath+".assignableBy["+strconv.Itoa(ai)+"].group", `unknown group: "`+grp+`"`)
					}
				}
			}
		}

		// permissions: rule 1 + namespace(can_<name>) + rule 5 + rule 4(inherit) + rule 7.
		for pei, perm := range r.Permissions {
			ppath := base + ".permissions[" + strconv.Itoa(pei) + "]"
			checkName(perm.Name, ppath+".name")
			claimRelation("can_"+perm.Name, ppath+".name")

			if len(perm.GrantedByRoles) == 0 {
				add("EMPTY_GRANT", ppath+".grantedByRoles", "permission must be granted by >= 1 role")
			}
			for gi, roleName := range perm.GrantedByRoles {
				if _, ok := roleNameSet[roleName]; !ok {
					add("UNKNOWN_ROLE", ppath+".grantedByRoles["+strconv.Itoa(gi)+"]", `unknown role: "`+roleName+`"`)
				}
			}

			for ii, rel := range perm.InheritFromParents {
				if _, ok := parentRelSeen[rel]; !ok {
					add("UNKNOWN_PARENT", ppath+".inheritFromParents["+strconv.Itoa(ii)+"]", `unknown parent relation: "`+rel+`"`)
					continue
				}
				// rule 7: 상속 부모의 모든 parentTypes가 동명 permission을 가져야 함.
				for _, p := range r.Parents {
					if p.RelationName != rel {
						continue
					}
					for _, pt := range p.ParentTypes {
						perms, ok := resourcePerms[pt]
						if ok {
							if _, has := perms[perm.Name]; !has {
								add("PARENT_MISSING_PERMISSION", ppath+".inheritFromParents["+strconv.Itoa(ii)+"]",
									`parent type "`+pt+`" (via "`+rel+`") has no permission "`+perm.Name+`"; `+
										`"can_`+perm.Name+` from `+rel+`" would be invalid in OpenFGA`)
							}
						}
					}
					break // find: 첫 매칭만.
				}
			}
		}
	}

	// 조건 정의 + SubjectRef.condition 참조 검증(LFGA-14).
	conditionNames := make(map[string]struct{})
	for ci, c := range ir.Conditions {
		if _, ok := conditionNames[c.Name]; ok {
			add("DUP_CONDITION", "conditions["+strconv.Itoa(ci)+"].name", `duplicate condition: "`+c.Name+`"`)
		}
		conditionNames[c.Name] = struct{}{}
		cc := c
		for _, ce := range ValidateConditionDef(&cc) {
			add(ce.Code, "conditions["+strconv.Itoa(ci)+"]."+ce.Path, ce.Message)
		}
	}

	checkRefCondition := func(ref SubjectRef, path string) {
		if ref.Condition != nil {
			if _, ok := conditionNames[*ref.Condition]; !ok {
				add("CONDITION_UNKNOWN", path+".condition", `unknown condition: "`+*ref.Condition+`"`)
			}
		}
	}
	for gi, g := range ir.Groups {
		for mi, ref := range g.MemberTypes {
			checkRefCondition(ref, "groups["+strconv.Itoa(gi)+"].memberTypes["+strconv.Itoa(mi)+"]")
		}
	}
	for ri, r := range ir.Resources {
		for roi, role := range r.Roles {
			for ai, ref := range role.AssignableBy {
				checkRefCondition(ref, "resources["+strconv.Itoa(ri)+"].roles["+strconv.Itoa(roi)+"].assignableBy["+strconv.Itoa(ai)+"]")
			}
		}
	}

	return errs
}
