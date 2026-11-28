package annals

import "github.com/orchard-suite/platformkit/v3/change"

// StatementOf rebuilds the statement a change.stated record keeps, as
// Annals' docs/event.md maps one onto an event: the actor, the effects with
// their claims, the applier label, and the statement part, things and all.
// A record stored before its statement part held things has each catalog
// item as a thing; one stored before events had a statement part has none
// of its key, writer version, artifact, claim rules, or unknown parts, and
// its writer's name is its writer label.
func (e *Event) StatementOf() change.Statement {
	st := change.Statement{
		Writer:  change.Writer{Name: e.Labels["writer"]},
		Actor:   change.Actor{Kind: e.Actor.Kind, ID: e.Actor.ID, OnBehalfOf: e.Actor.OnBehalfOf},
		Applier: e.Labels["applier"],
	}
	if s := e.Statement; s != nil {
		st.Key, st.Writer, st.Artifact = s.Key, s.Writer, s.Artifact
		st.Things = append([]change.Ref(nil), s.Things...)
		st.Unknown = append([]change.Unknown(nil), s.Unknown...)
	}
	if len(st.Things) == 0 {
		for _, it := range e.Items {
			if it.System == "catalog" {
				st.Things = append(st.Things, change.Ref{Blueprint: it.Kind, Key: it.ID})
			}
		}
	}
	for _, ef := range e.Effects {
		st.Effects = append(st.Effects, change.Effect{
			Kind:    ef.Kind,
			Subject: change.Ref{Blueprint: ef.Subject.Blueprint, Key: ef.Subject.Key},
			Path:    ef.Path,
			Size:    ef.Size,
			Undo:    ef.Undo,
			Claim:   change.Claim{From: ef.Claim, Rule: ef.ClaimRule, Version: ef.ClaimVersion},
		})
	}
	return st
}
