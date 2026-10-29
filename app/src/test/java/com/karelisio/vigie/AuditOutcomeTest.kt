package com.karelisio.vigie

import com.karelisio.vigie.ui.settings.AuditOutcome
import com.karelisio.vigie.ui.settings.auditOutcome
import org.junit.Assert.assertEquals
import org.junit.Test

class AuditOutcomeTest {
    @Test
    fun outcomes() {
        assertEquals(AuditOutcome.Ok, auditOutcome("ok"))
        assertEquals(AuditOutcome.Ok, auditOutcome("ok — 5 images corrigées · 154 failles corrigées"))
        assertEquals(AuditOutcome.Started, auditOutcome("lancé"))
        assertEquals(AuditOutcome.Started, auditOutcome("lancée"))
        assertEquals(AuditOutcome.Failed, auditOutcome("délai dépassé — Mise à jour de comet"))
        assertEquals(AuditOutcome.Failed, auditOutcome("échec : no such container"))
    }
}
